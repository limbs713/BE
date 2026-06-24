// Package rag 는 광고 문구 위험도 검토(RAG) 로직을 담습니다.
//
// 검색 전략 S6 (실험상 최고 정확도):
//   입력 -> 쿼리 재작성(연상 개념 확장) -> 임베딩
//        -> pgvector 벡터검색(embedding 컬럼) + 키워드 RRF + 날짜 부스트 융합 top-8
//        -> 연결된 전례 수집 -> LLM 판정.
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service 는 RAG 검토 의존성(DB 풀, OpenAI)을 묶습니다.
type Service struct {
	pool  *pgxpool.Pool
	store *store
	ai    *openAIClient
}

// NewService 는 환경변수에서 설정을 읽어 Service를 만듭니다.
//   - DATABASE_URL  : postgres 접속 문자열
//   - OPENAI_API_KEY: OpenAI API 키
//
// 사용이 끝나면 Close()로 DB 풀을 정리하세요.
func NewService(ctx context.Context) (*Service, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL 환경변수가 비어 있습니다")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY 환경변수가 비어 있습니다")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("DB 풀 생성 실패: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("DB 접속 확인 실패: %w", err)
	}

	return &Service{
		pool:  pool,
		store: &store{pool: pool},
		ai:    newOpenAIClient(os.Getenv("OPENAI_API_KEY")),
	}, nil
}

// Close 는 DB 커넥션 풀을 정리합니다.
func (s *Service) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Verdict 는 LLM이 내린 판정 결과입니다.
type Verdict struct {
	Risky     bool     `json:"risky"`
	RiskLevel string   `json:"risk_level"` // none | low | medium | high
	Reasons   []string `json:"reasons"`
	Advice    string   `json:"advice"`
}

// ReviewResult 는 /review 응답 전체입니다.
type ReviewResult struct {
	Input         string      `json:"input"`
	Verdict       Verdict     `json:"verdict"`
	RelatedTopics []Topic     `json:"related_topics"`
	Precedents    []Precedent `json:"precedents"`
}

// Review 는 입력 문구를 검토해 위험도 판정 결과를 반환합니다(전략 S6).
func (s *Service) Review(ctx context.Context, input string) (*ReviewResult, error) {
	// 1) 쿼리 재작성으로 recall 향상 -> 임베딩
	expanded := s.ai.Rewrite(ctx, input)
	vec, err := s.ai.Embed(ctx, expanded)
	if err != nil {
		return nil, fmt.Errorf("임베딩 실패: %w", err)
	}

	// 2) pgvector 후보 풀 -> 키워드/날짜 융합 top-8 (키워드/날짜는 원문 기준)
	cands, err := s.store.searchVector(ctx, vec, poolSize)
	if err != nil {
		return nil, fmt.Errorf("벡터 검색 실패: %w", err)
	}
	topics := fuseRank(cands, input, retrieveK)

	// 3) 연결된 전례 수집
	ids := make([]string, len(topics))
	for i, t := range topics {
		ids[i] = t.ID
	}
	precedents, err := s.store.precedentsFor(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("전례 조회 실패: %w", err)
	}

	// 4) LLM 판정
	verdict, err := s.judge(ctx, input, topics, precedents)
	if err != nil {
		return nil, fmt.Errorf("판정 실패: %w", err)
	}

	return &ReviewResult{
		Input:         input,
		Verdict:       verdict,
		RelatedTopics: topics,
		Precedents:    precedents,
	}, nil
}

const judgeSystem = `너는 한국 시장의 광고 카피 위험도 검수자다.
주어진 광고 문구가 역사적 비극, 재난, 차별, 젠더 갈등, 종교 등 민감한 주제를
가볍게 소비하거나 연상시켜 사회적 논란을 일으킬 위험이 있는지 판정한다.
참고로 제공되는 '관련 민감 주제'와 '실제 논란 전례'를 근거로 삼되,
유사도가 낮으면 무리하게 위험으로 몰지 말고 차분히 판단한다.
반드시 아래 JSON 스키마로만 답한다:
{"risky": bool, "risk_level": "none|low|medium|high", "reasons": [string], "advice": string}
reasons와 advice는 한국어로 작성한다.`

func (s *Service) judge(ctx context.Context, input string, topics []Topic, precedents []Precedent) (Verdict, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## 검수 대상 광고 문구\n%s\n\n", input)

	b.WriteString("## 관련 민감 주제 (융합 검색 결과)\n")
	if len(topics) == 0 {
		b.WriteString("(없음)\n")
	}
	for _, t := range topics {
		fmt.Fprintf(&b, "- [%s, 유사도 %.3f] %s: %s\n", t.Category, t.Similarity, t.Title, t.Description)
	}

	b.WriteString("\n## 실제 논란 전례 (위 주제에 연결된 사례)\n")
	if len(precedents) == 0 {
		b.WriteString("(연결된 전례 없음)\n")
	}
	for _, p := range precedents {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", p.Region, p.Title, p.Description)
	}

	raw, err := s.ai.Judge(ctx, judgeSystem, b.String())
	if err != nil {
		return Verdict{}, err
	}
	var v Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Verdict{}, fmt.Errorf("판정 JSON 파싱 실패: %w (원문: %s)", err, raw)
	}
	return v, nil
}

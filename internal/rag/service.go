// Package rag 는 광고 문구 위험도 검토(RAG) 로직을 담습니다.
//
// 검색 전략 S6 (실험상 최고 정확도):
//
//	입력 -> 쿼리 재작성(연상 개념 확장) -> 임베딩
//	     -> pgvector 벡터검색(embedding 컬럼) + 키워드 RRF + 날짜 부스트 융합 top-8
//	     -> 연결된 전례 수집 -> LLM 판정.
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
	Score     int      `json:"score"`      // 0~100 위험도 게이지
	Reasons   []string `json:"reasons"`
	Advice    string   `json:"advice"`
}

// Highlight 는 입력 문구 안의 위험 표현 한 건(인라인 하이라이트)입니다.
// UI는 phrase 구절에 severity 색/밑줄을 입히고 reason·basis·alt를 함께 보여줍니다.
type Highlight struct {
	Phrase     string  `json:"phrase"`
	Start      *int    `json:"start,omitempty"` // input 내 rune(문자) 오프셋. 매칭 실패 시 생략
	End        *int    `json:"end,omitempty"`
	Severity   string  `json:"severity"` // high | needs_review | low (UI: 위험/주의/낮음)
	Tag        string  `json:"tag,omitempty"`
	Category   string  `json:"category,omitempty"`
	Date       string  `json:"date,omitempty"`
	Reason     string  `json:"reason"`
	Basis      string  `json:"basis,omitempty"` // 근거(관련 주제/전례 출처)
	Confidence float64 `json:"confidence"`      // 신뢰도 0~1
	Alt        string  `json:"alt,omitempty"`   // 구절 단위 대체어
}

// Rewrite 는 안전 대체 문구 전체(Before→After)입니다.
type Rewrite struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// ReviewResult 는 /review 응답 전체입니다.
type ReviewResult struct {
	ID            string      `json:"id"`
	Input         string      `json:"input"`
	Verdict       Verdict     `json:"verdict"`
	Highlights    []Highlight `json:"highlights"`
	Rewrite       Rewrite     `json:"rewrite"`
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

	// 4) LLM 판정 (verdict + 위험 표현 하이라이트 + 안전 대체 문구)
	verdict, highlights, rewrite, err := s.judge(ctx, input, topics, precedents)
	if err != nil {
		return nil, fmt.Errorf("판정 실패: %w", err)
	}

	// 5) 후처리: score↔level 정합성 보정, highlight 오프셋 역매핑, rewrite 채움
	verdict.Score = clampScore(verdict.Score)
	verdict.RiskLevel = scoreToLevel(verdict.Score)
	// risky 는 score(=risk_level) 단일 기준으로 정한다. highlights 유무로 별도 분기하면
	// score=0(none)인데 risky=true 가 되는 모순이 생긴다.
	verdict.Risky = verdict.Score > 0
	// 같은 phrase 가 여러 번 등장할 때 순차 매칭되도록 phrase 별 검색 커서를 둔다.
	cursor := make(map[string]int, len(highlights))
	for i := range highlights {
		highlights[i].Severity = normalizeSeverity(highlights[i].Severity)
		ph := highlights[i].Phrase
		if start, end, endByte, ok := phraseOffsets(input, ph, cursor[ph]); ok {
			highlights[i].Start, highlights[i].End = &start, &end
			cursor[ph] = endByte
		}
	}
	// rewrite.before 는 항상 원문. after 가 비면(LLM 누락/안전 판정) 원문으로 폴백해
	// UI Before→After 가 빈 문자열로 교체를 제안하지 않도록 한다.
	rewrite.Before = input
	if strings.TrimSpace(rewrite.After) == "" {
		rewrite.After = input
	}

	// nil 슬라이스는 JSON 에서 null 로 직렬화돼 OpenAPI(required array)·UI 와 어긋나므로
	// 빈 슬라이스로 정규화한다(빈 지식베이스/하이라이트 없음 등 콜드 경로 방어).
	if topics == nil {
		topics = []Topic{}
	}
	if precedents == nil {
		precedents = []Precedent{}
	}
	if highlights == nil {
		highlights = []Highlight{}
	}

	return &ReviewResult{
		ID:            newReviewID(),
		Input:         input,
		Verdict:       verdict,
		Highlights:    highlights,
		Rewrite:       rewrite,
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
{
  "score": 0~100 정수(위험도. 0=완전 안전, 100=매우 위험),
  "reasons": [string],
  "advice": string,
  "highlights": [
    {
      "phrase": "문구에서 그대로 발췌한 위험 표현(원문 부분 문자열과 정확히 일치)",
      "severity": "high|needs_review|low",
      "tag": "분류 태그(예: 역사·인권, 신조어)",
      "category": "민감 주제 카테고리",
      "date": "관련 기념일/사건일 MM-DD (없으면 빈 문자열)",
      "reason": "왜 문제인지 한국어 설명",
      "basis": "근거가 된 관련 주제/전례",
      "confidence": 0~1 실수,
      "alt": "이 구절을 대체할 안전한 표현"
    }
  ],
  "rewrite": { "after": "문구 전체를 의미를 살려 안전하게 고쳐 쓴 버전" }
}
phrase는 반드시 입력 문구에 실제로 등장하는 부분 문자열을 그대로 발췌한다.
위험 표현이 없으면 highlights는 빈 배열, rewrite.after는 원문과 동일하게 둔다.
reasons, advice, reason, alt, rewrite.after는 한국어로 작성한다.`

// judgeOutput 은 판정 LLM의 원시 JSON 응답 스키마입니다.
type judgeOutput struct {
	Score      int      `json:"score"`
	Reasons    []string `json:"reasons"`
	Advice     string   `json:"advice"`
	Highlights []struct {
		Phrase     string  `json:"phrase"`
		Severity   string  `json:"severity"`
		Tag        string  `json:"tag"`
		Category   string  `json:"category"`
		Date       string  `json:"date"`
		Reason     string  `json:"reason"`
		Basis      string  `json:"basis"`
		Confidence float64 `json:"confidence"`
		Alt        string  `json:"alt"`
	} `json:"highlights"`
	Rewrite struct {
		After string `json:"after"`
	} `json:"rewrite"`
}

func (s *Service) judge(ctx context.Context, input string, topics []Topic, precedents []Precedent) (Verdict, []Highlight, Rewrite, error) {
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
		return Verdict{}, nil, Rewrite{}, err
	}
	var o judgeOutput
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return Verdict{}, nil, Rewrite{}, fmt.Errorf("판정 JSON 파싱 실패: %w (원문: %s)", err, raw)
	}

	verdict := Verdict{Score: o.Score, Reasons: o.Reasons, Advice: o.Advice}
	highlights := make([]Highlight, 0, len(o.Highlights))
	for _, h := range o.Highlights {
		highlights = append(highlights, Highlight{
			Phrase:     h.Phrase,
			Severity:   h.Severity,
			Tag:        h.Tag,
			Category:   h.Category,
			Date:       h.Date,
			Reason:     h.Reason,
			Basis:      h.Basis,
			Confidence: h.Confidence,
			Alt:        h.Alt,
		})
	}
	rewrite := Rewrite{After: o.Rewrite.After}
	return verdict, highlights, rewrite, nil
}

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
	"golang.org/x/sync/errgroup"
)

// Service 는 RAG 검토 의존성(DB 풀, OpenAI)을 묶습니다.
type Service struct {
	pool  pgxPool
	store *store
	ai    aiClient
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
// 4개 임베딩 테이블을 각각 벡터 검색해 유사 항목을 함께 반환합니다.
type ReviewResult struct {
	ID            string        `json:"id"`
	Input         string        `json:"input"`
	Verdict       Verdict       `json:"verdict"`
	Highlights    []Highlight   `json:"highlights"`
	Rewrite       Rewrite       `json:"rewrite"`
	RelatedTopics []Topic       `json:"related_topics"` // sensitive_events (벡터+키워드+날짜 융합)
	RelatedIssues []RelatedItem `json:"related_issues"` // sensitive_issues (벡터)
	RelatedSlang  []RelatedItem `json:"related_slang"`  // slang_terms (벡터)
	RelatedTrends []RelatedItem `json:"related_trends"` // mim_terms (벡터)
}

// Review 는 입력 문구를 검토해 위험도 판정 결과를 반환합니다(전략 S6).
func (s *Service) Review(ctx context.Context, input string) (*ReviewResult, error) {
	// 1) 쿼리 재작성으로 recall 향상 -> 임베딩
	expanded := s.ai.Rewrite(ctx, input)
	vec, err := s.ai.Embed(ctx, expanded)
	if err != nil {
		return nil, fmt.Errorf("임베딩 실패: %w", err)
	}

	// 2) 4개 임베딩 테이블을 병렬로 벡터 검색한다.
	//    - sensitive_events 는 벡터 후보 풀 -> 키워드/날짜 융합(fuseRank)으로 정밀화
	//    - 나머지(issues/slang/mim)는 임베딩 코사인 top-K 직접 검색
	var (
		topics                []Topic
		issues, slang, trends []RelatedItem
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		cands, err := s.store.searchVector(gctx, vec, poolSize)
		if err != nil {
			return fmt.Errorf("민감주제 검색 실패: %w", err)
		}
		topics = fuseRank(cands, input, retrieveK)
		return nil
	})
	g.Go(func() error {
		r, err := s.store.searchSimilar(gctx, specIssues, vec, relatedK)
		if err != nil {
			return fmt.Errorf("전례 검색 실패: %w", err)
		}
		issues = r
		return nil
	})
	g.Go(func() error {
		r, err := s.store.searchSimilar(gctx, specSlang, vec, relatedK)
		if err != nil {
			return fmt.Errorf("신조어 검색 실패: %w", err)
		}
		slang = r
		return nil
	})
	g.Go(func() error {
		r, err := s.store.searchSimilar(gctx, specMim, vec, relatedK)
		if err != nil {
			return fmt.Errorf("유행어 검색 실패: %w", err)
		}
		trends = r
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// 3) LLM 판정 (verdict + 위험 표현 하이라이트 + 안전 대체 문구)
	verdict, highlights, rewrite, err := s.judge(ctx, input, topics, issues, slang, trends)
	if err != nil {
		return nil, fmt.Errorf("판정 실패: %w", err)
	}

	// 5) 후처리: score↔level 정합성 보정, highlight 오프셋 역매핑, rewrite 채움
	verdict.Score = clampScore(verdict.Score)
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
	// 하이라이트 심각도 ↔ 전체 score 정합: 가장 심각한 표현의 밴드 하한까지 score 를 끌어올려
	// "전체는 안전인데 주의/위험 표현이 있는" 모순을 없앤다(게이지 라벨과 칩을 일치시킴).
	if floor := severityScoreFloor(highlights); verdict.Score < floor {
		verdict.Score = floor
	}
	verdict.RiskLevel = scoreToLevel(verdict.Score)
	// risky 는 score(=risk_level) 단일 기준으로 정한다. highlights 유무로 별도 분기하면
	// score=0(none)인데 risky=true 가 되는 모순이 생긴다.
	verdict.Risky = verdict.Score > 0
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
	if issues == nil {
		issues = []RelatedItem{}
	}
	if slang == nil {
		slang = []RelatedItem{}
	}
	if trends == nil {
		trends = []RelatedItem{}
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
		RelatedIssues: issues,
		RelatedSlang:  slang,
		RelatedTrends: trends,
	}, nil
}

const judgeSystem = `너는 한국 시장의 광고 카피 위험도 검수자다.
주어진 광고 문구가 역사적 비극, 재난, 차별, 젠더 갈등, 종교 등 민감한 주제를
가볍게 소비하거나 연상시켜 사회적 논란을 일으킬 위험이 있는지 판정한다.
제공되는 '관련 민감 주제', '실제 논란 전례', '관련 신조어/은어', '관련 유행어/밈'은 단순 참고가 아니라
핵심 탐지 근거다. 각 검색 결과가 '왜 이 광고와 함께 검색됐는지'를 추론해, 그 매칭을 유발한 입력 속
표현(단어·숫자·구절)을 찾아낸다. 다만 유사도가 매우 낮고 입력과 실제 연결점이 없으면 무리하게 몰지 않는다.

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
[하이라이트 규칙]
입력의 모든 표현(단어·구·숫자·날짜·코드 포함)을 "이것이 민감 주제(역사적 비극·재난·차별·젠더·정치·종교 등)를 직접 가리키거나 맥락상 연상시키는가?"라는 한 가지 기준으로 검토한다. 특정 브랜드·사례에 맞춘 규칙이 아니라 일반 원리로 판단하며, 아래 예시는 이해를 돕는 예일 뿐 그 사례에 한정하지 않는다.

severity 는 위험의 '직접성'으로 정한다:
- high(위험): 표현이 민감 사건·날짜·상징·인물을 직접 가리킨다(역사적 사건·민주화운동·참사·그 날짜 등).
- needs_review(주의): 그 자체로는 중립이나, 같은 문구의 민감 맥락과 연결되거나 통상적이지 않게 쓰여 위험을 연상시킨다.
- low(낮음): 관련성이 약하다.

판단 원칙:
0. 검색된 '관련 민감 주제/전례'마다 그것과 연결되는 입력 속 표현을 찾아 highlight로 표시하고 basis에 근거(주제/전례명)를 적는다. 검색 결과 제목·설명에 나오는 핵심 단어가 입력에도 있으면 적극 대조해 표시한다(예: 전례가 '5·18 기념일 탱크 연출 논란'이면 입력의 '탱크'를, 전례가 '광복절 마케팅 논란'이면 입력의 '광복'·'0815'를 표시).
1. 어떤 표현을 위험으로 봤으면, 같은 문구에서 그 맥락과 연결되는 다른 표현(반복·축약·파생·완곡)도 스스로 따져 최소 needs_review 로 표시한다.
2. 숫자·날짜·코드도 예외 없이 검토한다. 가격·할인·수량·용량으로 자연스러우면 안전이지만, 그 값이 민감한 날짜·사건·인물·은어로 읽히거나(예: 역사적 날짜를 수량·가격·할인율로 위장), 맥락상 부자연스럽게 배치돼 의도가 의심되면 표시한다. 제공된 '관련 민감 주제'의 날짜·키워드와 입력의 숫자·표현을 대조하라.
3. 의심되면 표시하는 쪽을 택한다 — 누락보다 과표시가 안전하다.

phrase는 반드시 입력 문구에 등장하는 그대로의 부분 문자열(공백·기호 포함)이어야 한다(예: 입력이 "5 18"이면 "5·18"이 아니라 "5 18"로 발췌).
같은 표현이 여러 번 나오면 각 위치를 모두 별개의 highlight 로 표시한다.
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

func (s *Service) judge(ctx context.Context, input string, topics []Topic, issues, slang, trends []RelatedItem) (Verdict, []Highlight, Rewrite, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## 검수 대상 광고 문구\n%s\n\n", input)

	b.WriteString("## 관련 민감 주제 (융합 검색 결과)\n")
	if len(topics) == 0 {
		b.WriteString("(없음)\n")
	}
	for _, t := range topics {
		fmt.Fprintf(&b, "- [%s, 유사도 %.3f] %s: %s\n", t.Category, t.Similarity, t.Title, t.Description)
	}

	// issues/slang/trends 는 RelatedItem 공통 형태라 한 헬퍼로 출력한다.
	writeItems := func(header, empty string, items []RelatedItem) {
		fmt.Fprintf(&b, "\n## %s\n", header)
		if len(items) == 0 {
			b.WriteString(empty + "\n")
		}
		for _, it := range items {
			fmt.Fprintf(&b, "- [유사도 %.3f] %s: %s\n", it.Similarity, it.Title, it.Snippet)
		}
	}
	writeItems("실제 논란 전례 (유사 사례)", "(유사 전례 없음)", issues)
	writeItems("관련 신조어/은어", "(관련 신조어 없음)", slang)
	writeItems("관련 유행어/밈", "(관련 유행어 없음)", trends)

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

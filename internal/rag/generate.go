package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Trend 는 sensitive_events.trigger_expressions 를 펼쳐 만든 트렌드어 한 건입니다.
//   - Tag: '#'을 붙인 표현(예: "#광복절")
//   - Category: 해당 표현이 속한 카테고리
//   - Up: 활성도(등장 빈도 기반 의사 점수)
//   - Rank: 활성도 내림차순 순위(1부터)
//   - Delta: 전주 대비 변동. 현재 시계열 스냅샷이 없어 0 으로 둔다(추후 산출).
type Trend struct {
	Tag      string `json:"tag"`
	Category string `json:"category"`
	Up       int    `json:"up"`
	Rank     int    `json:"rank"`
	Delta    int    `json:"delta"`
}

// trendingTerms 는 sensitive_events 의 trigger_expressions 배열을 표현 단위로 펼친 뒤,
// 표현별 등장 횟수(COUNT)를 활성도로 삼아 상위 limit 개를 반환합니다.
// trigger_expressions 가 NULL/빈 배열인 경우를 방어합니다.
func (s *store) trendingTerms(ctx context.Context, limit int) ([]Trend, error) {
	const q = `
		SELECT expr, category, COUNT(*) AS up
		FROM (
			SELECT TRIM(elem) AS expr, COALESCE(category, '') AS category
			FROM sensitive_events,
			     LATERAL jsonb_array_elements_text(
			         COALESCE(trigger_expressions, '[]'::jsonb)
			     ) AS elem
			WHERE trigger_expressions IS NOT NULL
			  AND jsonb_typeof(trigger_expressions) = 'array'
		) AS t
		WHERE expr <> ''
		GROUP BY expr, category
		ORDER BY up DESC, expr ASC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("트렌드어 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []Trend
	for rows.Next() {
		var (
			expr     string
			category string
			up       int
		)
		if err := rows.Scan(&expr, &category, &up); err != nil {
			return nil, fmt.Errorf("트렌드어 스캔 실패: %w", err)
		}
		// SQL 에서 TRIM 으로 앞뒤 공백을 이미 제거했다. 내부 공백까지 제거하면
		// "광복 절"과 "광복절"이 한 태그로 합쳐져 GROUP BY 와 어긋나(count 분산) 버리므로
		// 여기서는 추가 변형 없이 그대로 '#' 만 부착한다.
		if expr == "" {
			continue
		}
		out = append(out, Trend{
			Tag:      "#" + expr,
			Category: category,
			Up:       up,
			// 쿼리가 up DESC 로 정렬되므로 누적 인덱스가 곧 순위(1부터)다.
			Rank: len(out) + 1,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("트렌드어 행 순회 실패: %w", err)
	}
	return out, nil
}

// Trends 는 상위 limit 개의 트렌드어를 반환합니다(store 위임).
func (s *Service) Trends(ctx context.Context, limit int) ([]Trend, error) {
	return s.store.trendingTerms(ctx, limit)
}

const generateSystem = `너는 한국의 광고 카피라이터다.
주어진 제품/캠페인 정보와 톤, 그리고 활용할 트렌드어를 바탕으로
매력적인 광고 헤드라인 후보 4개를 만든다.
모든 문구는 한국어로 작성한다.
반드시 다음 JSON 형식으로만 응답한다(설명·코드블록·여분 텍스트 금지):
{"candidates": ["문구1","문구2","문구3","문구4"]}`

// Generate 는 제품/톤/트렌드어로 광고 헤드라인 후보를 생성합니다.
// chat(jsonMode=true) 결과를 JSON 파싱해 []string 으로 반환합니다.
func (c *openAIClient) Generate(ctx context.Context, product, tone string, trends []string) ([]string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "제품/캠페인: %s\n", strings.TrimSpace(product))
	if strings.TrimSpace(tone) != "" {
		fmt.Fprintf(&b, "톤: %s\n", strings.TrimSpace(tone))
	}
	if len(trends) > 0 {
		fmt.Fprintf(&b, "활용할 트렌드어: %s\n", strings.Join(trends, ", "))
	}
	b.WriteString("위 정보를 활용해 광고 헤드라인 후보 4개를 만들어라.")

	raw, err := c.chat(ctx, generateSystem, b.String(), true)
	if err != nil {
		return nil, fmt.Errorf("문구 생성 호출 실패: %w", err)
	}

	var parsed struct {
		Candidates []string `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("문구 생성 응답 파싱 실패: %w", err)
	}
	if len(parsed.Candidates) == 0 {
		return nil, fmt.Errorf("문구 생성 응답에 후보가 없습니다")
	}
	return parsed.Candidates, nil
}

// GenerateRequest 는 /generate 요청 본문입니다.
type GenerateRequest struct {
	Product string   `json:"product"`
	Tone    string   `json:"tone"`
	Trends  []string `json:"trends"`
}

// GenerateCandidate 는 생성된 문구 한 건과 자동 검토 결과입니다.
type GenerateCandidate struct {
	Text        string `json:"text"`
	Score       int    `json:"score"`
	SafetyLabel string `json:"safety_label"` // 안전 | 주의 | 위험 | 검토실패
	Note        string `json:"note"`
	ReviewID    string `json:"review_id,omitempty"` // 검토 실패 시 비어 있으므로 생략
}

// Generate 는 후보 문구를 생성하고, 각 후보를 자동 리스크 검토한 결과를 반환합니다.
//   - ai.Generate 로 후보 텍스트 생성
//   - 각 후보를 Review 로 검토하고 SaveHistory 로 이력 저장(실패는 무시)
//   - Verdict.Score 로 안전 라벨 산출
func (s *Service) Generate(ctx context.Context, req GenerateRequest) ([]GenerateCandidate, error) {
	texts, err := s.ai.Generate(ctx, req.Product, req.Tone, req.Trends)
	if err != nil {
		return nil, fmt.Errorf("문구 생성 실패: %w", err)
	}

	// 후보들은 서로 독립적이므로 병렬로 검토한다(각 Review 는 Rewrite+Embed+Judge
	// 3회 OpenAI 왕복이라 직렬화하면 후보 수에 비례해 지연이 커진다).
	// 결과 순서를 보존하기 위해 인덱스 슬롯에 기록한다.
	slots := make([]GenerateCandidate, len(texts))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for i, text := range texts {
		i, text := i, strings.TrimSpace(text)
		if text == "" {
			continue
		}
		g.Go(func() error {
			// 후보 1건 검토가 실패해도 전체를 버리지 않고 해당 후보만 '검토실패'로
			// 표기해 부분 결과를 반환한다(에러를 errgroup 으로 전파하지 않음).
			start := time.Now()
			review, err := s.Review(gctx, text)
			if err != nil {
				slots[i] = GenerateCandidate{
					Text:        text,
					SafetyLabel: "검토실패",
					Note:        "리스크 검토 중 오류가 발생했습니다",
				}
				return nil
			}
			latencyMs := int(time.Since(start).Milliseconds())

			// 검토 이력 저장(베스트에포트). 요청 컨텍스트가 취소돼도 저장되도록
			// review.go 와 동일하게 별도의 짧은 타임아웃 컨텍스트를 쓴다.
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.SaveHistory(saveCtx, review, "generate", latencyMs)
			cancel()

			slots[i] = GenerateCandidate{
				Text:        text,
				Score:       review.Verdict.Score,
				SafetyLabel: safetyLabel(review.Verdict.Score),
				Note:        safetyNote(review.Verdict),
				ReviewID:    review.ID,
			}
			return nil
		})
	}
	_ = g.Wait() // 모든 작업이 nil 을 반환하므로 에러 없음(부분 실패는 슬롯에 표기)

	// 빈 text 로 건너뛴 슬롯을 제외하고 순서대로 모은다.
	out := make([]GenerateCandidate, 0, len(slots))
	for _, c := range slots {
		if c.Text != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// safetyNote 는 판정의 조언 또는 첫 사유를 노트로 사용합니다(둘 다 없으면 기본 문구).
// 안전(score<34, 라벨 '안전')인 후보는 경고성 advice 를 숨겨 라벨과 노트가
// 어긋나지 않게 합니다("안전"인데 경고문이 붙는 모순 방지).
func safetyNote(v Verdict) string {
	if v.Score < 34 {
		return "민감 표현 없음"
	}
	if strings.TrimSpace(v.Advice) != "" {
		return v.Advice
	}
	if len(v.Reasons) > 0 && strings.TrimSpace(v.Reasons[0]) != "" {
		return v.Reasons[0]
	}
	return "민감 표현 없음"
}

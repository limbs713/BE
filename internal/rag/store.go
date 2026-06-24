package rag

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Topic 은 sensitive_events(민감 주제 마스터)에서 검색된 한 건입니다.
type Topic struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Category    string  `json:"category"`
	EventDate   string  `json:"event_date"`
	Description string  `json:"description"`
	Similarity  float64 `json:"similarity"`
}

// topicData 는 융합 랭킹에 쓰는 내부 후보(키워드 포함)입니다.
type topicData struct {
	Topic
	triggers []string
}

// Precedent 은 sensitive_issues(실제 논란 사례)에서 가져온 전례입니다.
type Precedent struct {
	IssueID     string `json:"issue_id"`
	Region      string `json:"region"`
	Title       string `json:"title"`
	Description string `json:"description"`
	TopicID     string `json:"topic_id"` // 어떤 주제에 연결된 사례인지
}

// store 는 DB(pgvector)에서 검색·조회를 담당합니다.
type store struct {
	pool *pgxpool.Pool
}

// searchVector 는 pgvector 코사인 거리로 주제를 정렬해 상위 limit개 후보를 반환합니다.
// (embedding 컬럼 사용. 거리 연산자 <=> 는 코사인 거리, HNSW 인덱스 활용.)
// 반환 슬라이스는 벡터 유사도 내림차순이며, 융합 랭킹은 Go(fuseRank)에서 수행합니다.
func (s *store) searchVector(ctx context.Context, vec []float32, limit int) ([]topicData, error) {
	const q = `
		SELECT id, title, category, COALESCE(event_date, ''),
		       COALESCE(trigger_expressions, '[]'::jsonb), description,
		       1 - (embedding <=> $1::vector) AS similarity
		FROM sensitive_events
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, vectorLiteral(vec), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []topicData
	for rows.Next() {
		var t topicData
		var trigRaw []byte
		if err := rows.Scan(&t.ID, &t.Title, &t.Category, &t.EventDate, &trigRaw, &t.Description, &t.Similarity); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(trigRaw, &t.triggers) // jsonb 배열 -> []string
		out = append(out, t)
	}
	return out, rows.Err()
}

// precedentsFor 는 주어진 주제 ID들에 FK로 연결된 실제 사례를 모읍니다.
// (sensitive_issues.event_id -> sensitive_events.id)
func (s *store) precedentsFor(ctx context.Context, topicIDs []string) ([]Precedent, error) {
	if len(topicIDs) == 0 {
		return nil, nil
	}
	const q = `
		SELECT issue_id, region, title, description, event_id
		FROM sensitive_issues
		WHERE event_id = ANY($1)
		ORDER BY issue_id`
	rows, err := s.pool.Query(ctx, q, topicIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ps []Precedent
	for rows.Next() {
		var p Precedent
		if err := rows.Scan(&p.IssueID, &p.Region, &p.Title, &p.Description, &p.TopicID); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

// vectorLiteral 는 []float32 를 pgvector 텍스트 리터럴("[0.1,0.2,...]")로 만듭니다.
// 쿼리에서 $1::vector 로 캐스팅해 사용합니다.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

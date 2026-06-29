package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// RelatedItem 은 임베딩 테이블에서 벡터 유사도로 검색된 한 건입니다.
// (sensitive_issues / slang_terms / mim_terms 공용 결과 형태)
type RelatedItem struct {
	Source     string  `json:"source"` // 출처 테이블
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Category   string  `json:"category,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
	Similarity float64 `json:"similarity"`
}

// searchSpec 은 벡터 검색 대상 테이블의 컬럼 매핑입니다.
// 컬럼명·식은 모두 아래 상수에서만 오므로 SQL 조합은 안전합니다.
type searchSpec struct {
	source      string // 결과 Source 라벨(= 테이블명)
	table       string
	idCol       string
	titleExpr   string
	catExpr     string
	snippetExpr string
}

var (
	specIssues = searchSpec{"sensitive_issues", "sensitive_issues", "issue_id", "title", "COALESCE(category,'')", "COALESCE(NULLIF(new_description,''), description, '')"}
	specSlang  = searchSpec{"slang_terms", "slang_terms", "id", "expression", "COALESCE(nuance,'')", "COALESCE(meaning,'')"}
	specMim    = searchSpec{"mim_terms", "mim_terms", "id", "COALESCE(word,'')", "''", "COALESCE(definition,'')"}
)

// store 는 DB(pgvector)에서 검색·조회를 담당합니다.
type store struct {
	pool pgxPool
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

// searchSimilar 는 spec 테이블에서 pgvector 코사인 거리로 상위 limit개를 검색합니다.
// (embedding 컬럼 + HNSW 인덱스 사용. 모든 임베딩 테이블에 공통 적용.)
func (s *store) searchSimilar(ctx context.Context, spec searchSpec, vec []float32, limit int) ([]RelatedItem, error) {
	q := fmt.Sprintf(`
		SELECT %s::text, %s, %s, %s, 1 - (embedding <=> $1::vector) AS similarity
		FROM %s
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`, spec.idCol, spec.titleExpr, spec.catExpr, spec.snippetExpr, spec.table)
	rows, err := s.pool.Query(ctx, q, vectorLiteral(vec), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RelatedItem
	for rows.Next() {
		it := RelatedItem{Source: spec.source}
		if err := rows.Scan(&it.ID, &it.Title, &it.Category, &it.Snippet, &it.Similarity); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
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

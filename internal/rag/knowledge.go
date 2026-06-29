package rag

import (
	"context"
	"fmt"
	"time"
)

// embedBatchSize 는 한 번에 임베딩 요청을 묶는 행 수입니다.
const embedBatchSize = 64

// SyncResult 는 지식베이스 동기화 결과 요약입니다.
type SyncResult struct {
	Synced       int    `json:"synced"`
	TotalEvents  int    `json:"total_events"`
	LastSyncedAt string `json:"last_synced_at"`
}

// KnowledgeStatus 는 현재 지식베이스 상태(마지막 동기화 시각, 건수)입니다.
type KnowledgeStatus struct {
	LastSyncedAt string `json:"last_synced_at"`
	TotalEvents  int    `json:"total_events"`
	TotalIssues  int    `json:"total_issues"`
}

// pendingEvent 는 임베딩이 비어 있어 동기화 대상이 되는 행입니다.
type pendingEvent struct {
	id          string
	title       string
	description string
}

// SyncKnowledge 는 embedding 이 비어 있는 sensitive_events 행을 임베딩해 채우고,
// kb_sync_meta.last_synced_at 을 갱신합니다.
// 임베딩 대상이 0개여도 정상 동작하며 last_synced_at 은 항상 갱신합니다.
func (s *Service) SyncKnowledge(ctx context.Context) (*SyncResult, error) {
	pending, err := s.pendingEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("임베딩 대상 조회 실패: %w", err)
	}

	synced := 0
	for start := 0; start < len(pending); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]

		texts := make([]string, len(batch))
		for i, ev := range batch {
			texts[i] = ev.title + " " + ev.description
		}

		vecs, err := s.ai.EmbedBatch(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("임베딩 생성 실패: %w", err)
		}

		for i, ev := range batch {
			if err := s.updateEventEmbedding(ctx, ev.id, vecs[i]); err != nil {
				return nil, fmt.Errorf("임베딩 저장 실패(id=%s): %w", ev.id, err)
			}
			synced++
		}
	}

	syncedAt, err := s.touchSyncMeta(ctx)
	if err != nil {
		return nil, fmt.Errorf("동기화 시각 갱신 실패: %w", err)
	}

	total, err := s.countEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("이벤트 건수 조회 실패: %w", err)
	}

	return &SyncResult{
		Synced:       synced,
		TotalEvents:  total,
		LastSyncedAt: syncedAt.Format(time.RFC3339),
	}, nil
}

// KnowledgeStatus 는 마지막 동기화 시각과 이벤트/사례 건수를 반환합니다.
func (s *Service) KnowledgeStatus(ctx context.Context) (*KnowledgeStatus, error) {
	var lastSynced string
	var ts *time.Time
	const q = `SELECT last_synced_at FROM kb_sync_meta WHERE id = 1`
	if err := s.pool.QueryRow(ctx, q).Scan(&ts); err != nil {
		return nil, fmt.Errorf("동기화 메타 조회 실패: %w", err)
	}
	if ts != nil {
		lastSynced = ts.Format(time.RFC3339)
	}

	totalEvents, err := s.countEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("이벤트 건수 조회 실패: %w", err)
	}
	totalIssues, err := s.countIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("사례 건수 조회 실패: %w", err)
	}

	return &KnowledgeStatus{
		LastSyncedAt: lastSynced,
		TotalEvents:  totalEvents,
		TotalIssues:  totalIssues,
	}, nil
}

// pendingEvents 는 embedding 이 NULL 인 sensitive_events 행을 조회합니다.
func (s *Service) pendingEvents(ctx context.Context) ([]pendingEvent, error) {
	const q = `
		SELECT id, title, COALESCE(description, '')
		FROM sensitive_events
		WHERE embedding IS NULL
		ORDER BY id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pendingEvent
	for rows.Next() {
		var ev pendingEvent
		if err := rows.Scan(&ev.id, &ev.title, &ev.description); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// updateEventEmbedding 는 한 행의 embedding 컬럼을 갱신합니다.
func (s *Service) updateEventEmbedding(ctx context.Context, id string, vec []float32) error {
	const q = `UPDATE sensitive_events SET embedding = $1::vector WHERE id = $2`
	_, err := s.pool.Exec(ctx, q, vectorLiteral(vec), id)
	return err
}

// touchSyncMeta 는 kb_sync_meta.last_synced_at 을 now() 로 갱신하고 그 시각을 반환합니다.
// 행이 없으면 새로 INSERT 합니다.
func (s *Service) touchSyncMeta(ctx context.Context) (time.Time, error) {
	const q = `
		INSERT INTO kb_sync_meta (id, last_synced_at)
		VALUES (1, now())
		ON CONFLICT (id) DO UPDATE SET last_synced_at = now()
		RETURNING last_synced_at`
	var ts time.Time
	if err := s.pool.QueryRow(ctx, q).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	return ts, nil
}

// countEvents 는 sensitive_events 전체 건수를 반환합니다.
func (s *Service) countEvents(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sensitive_events`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// countIssues 는 sensitive_issues 전체 건수를 반환합니다.
func (s *Service) countIssues(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sensitive_issues`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

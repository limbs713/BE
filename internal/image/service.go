package image

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const timeFormat = time.RFC3339

type Service struct {
	pool   *pgxpool.Pool
	clova  *clovaClient
}

const (
	OCRProviderClova = "clova"
	OCRStatusPending = "pending"
	OCRStatusProcessing = "processing"
	OCRStatusDone = "done"
	OCRStatusFailed = "failed"
)

type UploadInput struct {
	FileName    string
	ContentType string
	Data        []byte
}

type UploadResult struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	FileSize    int    `json:"file_size_bytes"`
	OCRProvider string `json:"ocr_provider"`
	OCRStatus   string `json:"ocr_status"`
	OCRText     string `json:"ocr_text,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func NewService(ctx context.Context) (*Service, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL 환경변수가 비어 있습니다")
	}
	clovaURL := os.Getenv("CLOVA_OCR_URL")
	if clovaURL == "" {
		return nil, fmt.Errorf("CLOVA_OCR_URL 환경변수가 비어 있습니다")
	}
	clovaSecret := os.Getenv("CLOVA_OCR_SECRET")
	if clovaSecret == "" {
		return nil, fmt.Errorf("CLOVA_OCR_SECRET 환경변수가 비어 있습니다")
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
		clova: newClovaClient(clovaURL, clovaSecret),
	}, nil
}

func (s *Service) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Service) SaveImage(ctx context.Context, in UploadInput) (UploadResult, error) {
	const q = `
		INSERT INTO ad_images (file_name, content_type, file_size_bytes, image_data, ocr_provider)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, created_at`

	var out UploadResult
	var createdAt time.Time
	err := s.pool.QueryRow(ctx, q, in.FileName, in.ContentType, len(in.Data), in.Data, OCRProviderClova).Scan(&out.ID, &createdAt)
	if err != nil {
		return UploadResult{}, fmt.Errorf("이미지 저장 실패: %w", err)
	}

	out.FileName = in.FileName
	out.ContentType = in.ContentType
	out.FileSize = len(in.Data)
	out.OCRProvider = OCRProviderClova
	out.OCRStatus = OCRStatusPending
	out.CreatedAt = createdAt.Format(timeFormat)

	return out, nil
}

func (s *Service) SaveImageAndRunOCR(ctx context.Context, in UploadInput) (UploadResult, error) {
	out, err := s.SaveImage(ctx, in)
	if err != nil {
		return UploadResult{}, err
	}

	if err := s.updateStatus(ctx, out.ID, OCRStatusProcessing, "", nil, ""); err != nil {
		return UploadResult{}, err
	}

	ocrResult, err := s.clova.ExtractText(ctx, in.FileName, in.Data)
	if err != nil {
		updateErr := s.updateStatus(ctx, out.ID, OCRStatusFailed, "", nil, err.Error())
		if updateErr != nil {
			return UploadResult{}, fmt.Errorf("OCR 실패(%v), 상태 저장 실패(%v)", err, updateErr)
		}
		return UploadResult{}, err
	}

	if err := s.updateStatus(ctx, out.ID, OCRStatusDone, ocrResult.ExtractedText, ocrResult.RawResponse, ""); err != nil {
		return UploadResult{}, err
	}
	if err := s.updateClovaRequestID(ctx, out.ID, ocrResult.RequestID); err != nil {
		return UploadResult{}, err
	}

	out.OCRStatus = OCRStatusDone
	out.OCRText = ocrResult.ExtractedText
	return out, nil
}

func (s *Service) updateStatus(ctx context.Context, id, status, text string, rawResponse []byte, lastError string) error {
	const q = `
		UPDATE ad_images
		SET ocr_status = $2,
		    ocr_text = NULLIF($3, ''),
		    clova_raw_response = CASE WHEN $4::jsonb IS NULL THEN clova_raw_response ELSE $4::jsonb END,
		    last_error = NULLIF($5, ''),
		    processed_at = $6,
		    updated_at = NOW()
		WHERE id = $1::uuid`

	var raw json.RawMessage
	if len(rawResponse) > 0 {
		raw = json.RawMessage(rawResponse)
	}
	processedAt := time.Now().UTC()
	if status == OCRStatusProcessing {
		processedAt = time.Time{}
	}

	_, err := s.pool.Exec(ctx, q, id, status, text, raw, lastError, nullableTime(processedAt))
	if err != nil {
		return fmt.Errorf("이미지 상태 업데이트 실패: %w", err)
	}
	return nil
}

func (s *Service) updateClovaRequestID(ctx context.Context, id, requestID string) error {
	const q = `
		UPDATE ad_images
		SET clova_request_id = $2,
		    updated_at = NOW()
		WHERE id = $1::uuid`

	_, err := s.pool.Exec(ctx, q, id, requestID)
	if err != nil {
		return fmt.Errorf("CLOVA request id 저장 실패: %w", err)
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

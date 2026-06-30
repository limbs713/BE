package image

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type OCRResult struct {
	RequestID     string
	ExtractedText string
	RawResponse   []byte
}

type clovaClient struct {
	url        string
	secret     string
	httpClient *http.Client
}

type clovaMessage struct {
	Version   string            `json:"version"`
	RequestID string            `json:"requestId"`
	Timestamp int64             `json:"timestamp"`
	Images    []clovaImageField `json:"images"`
}

type clovaImageField struct {
	Format string `json:"format"`
	Name   string `json:"name"`
}

type clovaResponse struct {
	Images []struct {
		Fields []struct {
			InferText string `json:"inferText"`
		} `json:"fields"`
	} `json:"images"`
}

func newClovaClient(url, secret string) *clovaClient {
	return &clovaClient{
		url:        url,
		secret:     secret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *clovaClient) ExtractText(ctx context.Context, fileName string, imageData []byte) (OCRResult, error) {
	requestID := fmt.Sprintf("clova-%d", time.Now().UnixNano())
	message := clovaMessage{
		Version:   "V2",
		RequestID: requestID,
		Timestamp: time.Now().UnixMilli(),
		Images: []clovaImageField{{
			Format: detectImageFormat(fileName),
			Name:   "upload",
		}},
	}

	messageJSON, err := json.Marshal(message)
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA 요청 본문 생성 실패: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	messagePart, err := writer.CreateFormField("message")
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA message 파트 생성 실패: %w", err)
	}
	if _, err := messagePart.Write(messageJSON); err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA message 파트 쓰기 실패: %w", err)
	}

	filePart, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA file 파트 생성 실패: %w", err)
	}
	if _, err := filePart.Write(imageData); err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA file 파트 쓰기 실패: %w", err)
	}

	if err := writer.Close(); err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA multipart 종료 실패: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, &body)
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA 요청 생성 실패: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-OCR-SECRET", c.secret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA 호출 실패: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA 응답 읽기 실패: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OCRResult{}, fmt.Errorf("CLOVA 호출 실패: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed clovaResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return OCRResult{}, fmt.Errorf("CLOVA 응답 파싱 실패: %w", err)
	}

	var texts []string
	for _, img := range parsed.Images {
		for _, field := range img.Fields {
			text := strings.TrimSpace(field.InferText)
			if text != "" {
				texts = append(texts, text)
			}
		}
	}

	return OCRResult{
		RequestID:     requestID,
		ExtractedText: strings.Join(texts, " "),
		RawResponse:   raw,
	}, nil
}

func detectImageFormat(fileName string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	switch ext {
	case "jpg":
		return "jpeg"
	case "jpeg", "png", "pdf", "tiff", "gif", "bmp":
		return ext
	default:
		return "png"
	}
}
package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestUploadImage_Validation(t *testing.T) {
	tests := []struct {
		name       string
		buildReq   func(t *testing.T) *http.Request
		wantStatus int
	}{
		{
			name: "image 필드 없음 → 400",
			buildReq: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/upload-image", nil)
				req.Header.Set("Content-Type", "multipart/form-data; boundary=----boundary")
				return req
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "빈 파일 → 400",
			buildReq: func(t *testing.T) *http.Request {
				var buf bytes.Buffer
				w := multipart.NewWriter(&buf)
				fw, _ := w.CreateFormFile("image", "empty.png")
				_ = fw // write nothing
				w.Close()
				req := httptest.NewRequest(http.MethodPost, "/upload-image", &buf)
				req.Header.Set("Content-Type", w.FormDataContentType())
				return req
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "이미지가 아닌 파일 → 400",
			buildReq: func(t *testing.T) *http.Request {
				var buf bytes.Buffer
				w := multipart.NewWriter(&buf)
				fw, _ := w.CreateFormFile("image", "file.txt")
				fw.Write([]byte("plain text content"))
				w.Close()
				req := httptest.NewRequest(http.MethodPost, "/upload-image", &buf)
				req.Header.Set("Content-Type", w.FormDataContentType())
				return req
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			// nil service is safe: all test cases return before calling the service
			r.POST("/upload-image", UploadImage(nil))

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, tt.buildReq(t))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

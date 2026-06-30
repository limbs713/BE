package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/limbs713/BE/internal/image"
)

const maxUploadSize = 5 << 20 // 5MB

// UploadImage stores an uploaded image in DB for later OCR processing.
func UploadImage(svc *image.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileHeader, err := c.FormFile("image")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image 파일이 필요합니다"})
			return
		}

		f, err := fileHeader.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "업로드 파일을 열 수 없습니다"})
			return
		}
		defer f.Close()

		data, err := io.ReadAll(io.LimitReader(f, maxUploadSize+1))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "파일 읽기에 실패했습니다"})
			return
		}
		if len(data) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "빈 파일은 업로드할 수 없습니다"})
			return
		}
		if len(data) > maxUploadSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "파일 크기는 5MB 이하여야 합니다"})
			return
		}

		contentType := fileHeader.Header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		if !strings.HasPrefix(contentType, "image/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "이미지 파일만 업로드할 수 있습니다"})
			return
		}

		out, err := svc.SaveImageAndRunOCR(c.Request.Context(), image.UploadInput{
			FileName:    fileHeader.Filename,
			ContentType: contentType,
			Data:        data,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, out)
	}
}

package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/limbs713/BE/internal/rag"
)

type reviewRequest struct {
	Text string `json:"text"`
}

// Review 는 광고 문구를 받아 RAG 검토(유사 주제 + 전례 + LLM 판정) 결과를 반환합니다.
// rag.Service 의존성을 클로저로 주입받습니다.
func Review(svc *rag.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reviewRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 요청 본문입니다"})
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text 필드는 비어 있을 수 없습니다"})
			return
		}

		result, err := svc.Review(c.Request.Context(), req.Text)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health reports whether the server is alive. 로드밸런서나 오케스트레이터(k8s 등)가
// 주기적으로 호출해 서버 상태를 확인하는 용도입니다.
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

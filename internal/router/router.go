package router

import (
	"github.com/gin-gonic/gin"

	"github.com/limbs713/BE/internal/handler"
)

// New creates and configures the Gin engine with all routes registered.
// 새로운 라우트는 여기에 추가하세요.
func New() *gin.Engine {
	r := gin.Default()

	r.GET("/health", handler.Health)

	return r
}

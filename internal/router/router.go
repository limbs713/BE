package router

import (
	"github.com/gin-gonic/gin"

	"github.com/limbs713/BE/internal/handler"
	"github.com/limbs713/BE/internal/image"
	"github.com/limbs713/BE/internal/rag"
)

// New creates and configures the Gin engine with all routes registered.
// 새로운 라우트는 여기에 추가하세요.
func New(ragSvc *rag.Service, imageSvc *image.Service) *gin.Engine {
	r := gin.Default()

	r.GET("/health", handler.Health)
	r.POST("/review", handler.Review(ragSvc))
	r.POST("/upload-image", handler.UploadImage(imageSvc))

	return r
}

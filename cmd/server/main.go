package main

import (
	"context"
	"log"

	"github.com/limbs713/BE/internal/image"
	"github.com/limbs713/BE/internal/rag"
	"github.com/limbs713/BE/internal/router"
)

func main() {
	ragSvc, err := rag.NewService(context.Background())
	if err != nil {
		log.Fatalf("rag service init failed: %v", err)
	}
	defer ragSvc.Close()

	imageSvc, err := image.NewService(context.Background())
	if err != nil {
		log.Fatalf("image service init failed: %v", err)
	}
	defer imageSvc.Close()

	r := router.New(ragSvc, imageSvc)

	const addr = ":8080"
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

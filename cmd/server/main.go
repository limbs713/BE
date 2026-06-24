package main

import (
	"context"
	"log"

	"github.com/limbs713/BE/internal/rag"
	"github.com/limbs713/BE/internal/router"
)

func main() {
	ragSvc, err := rag.NewService(context.Background())
	if err != nil {
		log.Fatalf("rag service init failed: %v", err)
	}
	defer ragSvc.Close()

	r := router.New(ragSvc)

	const addr = ":8080"
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

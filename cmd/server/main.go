package main

import (
	"log"

	"github.com/limbs713/BE/internal/router"
)

func main() {
	r := router.New()

	const addr = ":8080"
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

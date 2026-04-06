package main

import (
	"log"
	"os"

	"github.com/anish/omegle/backend/golang/internal/config"
	"github.com/anish/omegle/backend/golang/internal/server"
)

func main() {
	config.LoadDotEnvIfEnabled()

	svc := server.NewServer()

	host := os.Getenv("HOST")
	if host == "" {
		if os.Getenv("ENABLE_IPV6") == "true" {
			host = "::"
		} else {
			host = "0.0.0.0"
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Printf("Omegle Go Service (WebRTC + Moderation) listening on %s\n", host+":"+port)
	if err := svc.Run(host + ":" + port); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

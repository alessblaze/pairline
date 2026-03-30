package main

import (
	"log"
	"os"

	"github.com/anish/omegle/backend/golang/internal/server"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using default values")
	}

	svc := server.NewServer()

	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
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

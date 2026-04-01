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

	svc := server.NewAdminServer()

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
		port = "8083"
	}

	log.Printf("Omegle Go Admin Service listening on %s\n", host+":"+port)
	if err := svc.Run(host + ":" + port); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}

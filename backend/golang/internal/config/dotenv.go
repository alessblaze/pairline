package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

const SkipDotEnvEnvVar = "IGNORE_DOTENV"

func LoadDotEnvIfEnabled() {
	if os.Getenv(SkipDotEnvEnvVar) != "" {
		log.Printf("%s is set, skipping .env loading", SkipDotEnvEnvVar)
		return
	}

	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using default values")
	}
}

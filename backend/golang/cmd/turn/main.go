package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/anish/omegle/backend/golang/internal/config"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/turnservice"
)

func main() {
	config.LoadDotEnvIfEnabled()

	cfg := turnservice.LoadConfigFromEnv()
	redisClient := appredis.NewClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("Failed to close Redis: %v", err)
		}
	}()

	svc, err := turnservice.NewService(cfg, redisClient)
	if err != nil {
		log.Fatal("TURN relay failed to initialize:", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.HealthListenAddress != "" {
		healthServer := &http.Server{
			Addr: cfg.HealthListenAddress,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok","service":"pairline-go-turn","timestamp":` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `}`))
			}),
		}

		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := healthServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("TURN health server shutdown error: %v", err)
			}
		}()

		go func() {
			log.Printf("TURN health endpoint listening on %s", cfg.HealthListenAddress)
			if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("TURN health server stopped with error: %v", err)
				stop()
			}
		}()
	}

	if err := svc.Run(ctx); err != nil {
		log.Fatal("TURN relay stopped with error:", err)
	}

	log.Printf("TURN relay exited cleanly on signal from pid=%d", os.Getpid())
}

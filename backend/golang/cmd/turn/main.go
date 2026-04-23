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
	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/turnservice"
)

func main() {
	config.LoadDotEnvIfEnabled()

	cfg := turnservice.LoadConfigFromEnv()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		validator      turnservice.Validator
		validatorClose func() error
		err            error
	)

	traceShutdown := func(context.Context) error { return nil }
	if shutdown, initErr := observability.InitTracing(ctx, "pairline-go-turn"); initErr != nil {
		log.Printf("Failed to initialize tracing for TURN relay: %v", initErr)
	} else {
		traceShutdown = shutdown
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down TURN tracing: %v", err)
		}
	}()

	metricsShutdown := func(context.Context) error { return nil }
	if shutdown, initErr := observability.InitMetrics(ctx, "pairline-go-turn"); initErr != nil {
		log.Printf("Failed to initialize metrics for TURN relay: %v", initErr)
	} else {
		metricsShutdown = shutdown
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsShutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down TURN metrics: %v", err)
		}
	}()

	if cfg.ControlGRPCAddress != "" {
		validator, validatorClose, err = turnservice.NewGRPCValidator(ctx, cfg)
		if err != nil {
			log.Fatal("TURN control-plane client failed to initialize:", err)
		}
		defer func() {
			if validatorClose != nil {
				if err := validatorClose(); err != nil {
					log.Printf("Failed to close TURN control-plane client: %v", err)
				}
			}
		}()
	} else {
		redisClient := appredis.NewClient()
		defer func() {
			if err := redisClient.Close(); err != nil {
				log.Printf("Failed to close Redis: %v", err)
			}
		}()
		validator = &redisValidator{redisClient: redisClient}
	}

	svc, err := turnservice.NewService(cfg, validator)
	if err != nil {
		log.Fatal("TURN relay failed to initialize:", err)
	}

	if cfg.HealthListenAddress != "" {
		sharedSecret := os.Getenv("GOLANG_TURN_SHARED_SECRET")
		healthServer := &http.Server{
			Addr: cfg.HealthListenAddress,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					http.NotFound(w, r)
					return
				}

				if sharedSecret != "" && r.Header.Get("x-shared-secret") != sharedSecret {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

type redisValidator struct {
	redisClient *appredis.Client
}

func (v *redisValidator) ValidateTURNUsername(ctx context.Context, username string) (turnservice.ValidationResult, error) {
	return turnservice.ValidateTURNUsername(ctx, v.redisClient.GetClient(), username)
}

func (v *redisValidator) ReserveTURNAllocation(ctx context.Context, username string, limit int) (bool, error) {
	return turnservice.ReserveAllocationSlot(ctx, v.redisClient.GetClient(), username, limit)
}

func (v *redisValidator) ReleaseTURNAllocation(ctx context.Context, username string) error {
	return turnservice.ReleaseAllocationSlot(ctx, v.redisClient.GetClient(), username)
}

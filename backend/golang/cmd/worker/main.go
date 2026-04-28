// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anish/omegle/backend/golang/internal/automod"
	"github.com/anish/omegle/backend/golang/internal/config"
	"github.com/anish/omegle/backend/golang/internal/observability"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/reporting"
	"github.com/anish/omegle/backend/golang/internal/storage"
)

func main() {
	config.LoadDotEnvIfEnabled()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if shutdownTracing, err := observability.InitTracing(ctx, "pairline-go-worker"); err != nil {
		log.Printf("Warning: failed to initialize OpenTelemetry tracing: %v", err)
	} else {
		defer shutdownTracing(context.Background())
	}

	if shutdownMetrics, err := observability.InitMetrics(ctx, "pairline-go-worker"); err != nil {
		log.Printf("Warning: failed to initialize OpenTelemetry metrics: %v", err)
	} else {
		defer shutdownMetrics(context.Background())
	}

	db := storage.NewDatabase()
	redisClient := appredis.NewClient()

	autoModerator := automod.NewWorker(db.GetDB(), redisClient)
	autoModerator.Start(ctx)

	reportIngestWorker := reporting.NewIngestWorker(db, redisClient.GetClient(), autoModerator)
	reportIngestWorker.Start(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Omegle Go Database Worker started")
	<-sigChan
	log.Println("Shutting down worker...")
	cancel()

	time.Sleep(2 * time.Second)
	db.Close()
	redisClient.Close()
}

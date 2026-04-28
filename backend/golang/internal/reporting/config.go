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

package reporting

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	defaultIngestStream = "stream:reports:ingest"
	defaultWorkerGroup  = "db_workers"
)

func IngestStreamName() string {
	return defaultString(os.Getenv("REPORT_INGEST_STREAM"), defaultIngestStream)
}

func WorkerGroupName() string {
	return defaultString(os.Getenv("REPORT_WORKER_GROUP"), defaultWorkerGroup)
}

// IngestStreamMaxLen returns an optional soft stream cap.
// A value of 0 disables trimming entirely so reports are not dropped by default.
func IngestStreamMaxLen() int64 {
	raw := strings.TrimSpace(os.Getenv("REPORT_INGEST_STREAM_MAXLEN"))
	if raw == "" {
		return 0
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}

	return value
}

func Enqueue(ctx context.Context, client redis.UniversalClient, payload string) error {
	return client.XAdd(ctx, buildXAddArgs(payload)).Err()
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func buildXAddArgs(payload string) *redis.XAddArgs {
	args := &redis.XAddArgs{
		Stream: IngestStreamName(),
		Values: map[string]interface{}{
			"payload": payload,
		},
	}

	if maxLen := IngestStreamMaxLen(); maxLen > 0 {
		args.MaxLen = maxLen
		args.Approx = true
	}

	return args
}

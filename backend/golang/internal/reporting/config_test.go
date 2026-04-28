package reporting

import (
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestIngestStreamConfigDefaults(t *testing.T) {
	t.Setenv("REPORT_INGEST_STREAM", "")
	t.Setenv("REPORT_WORKER_GROUP", "")
	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "")

	if got := IngestStreamName(); got != "stream:reports:ingest" {
		t.Fatalf("IngestStreamName() = %q", got)
	}

	if got := WorkerGroupName(); got != "db_workers" {
		t.Fatalf("WorkerGroupName() = %q", got)
	}

	if got := IngestStreamMaxLen(); got != 0 {
		t.Fatalf("IngestStreamMaxLen() = %d, want 0", got)
	}
}

func TestIngestStreamMaxLenParsesPositiveValuesOnly(t *testing.T) {
	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "25000")
	if got := IngestStreamMaxLen(); got != 25000 {
		t.Fatalf("IngestStreamMaxLen() = %d, want 25000", got)
	}

	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "-1")
	if got := IngestStreamMaxLen(); got != 0 {
		t.Fatalf("IngestStreamMaxLen() with negative value = %d, want 0", got)
	}

	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "garbage")
	if got := IngestStreamMaxLen(); got != 0 {
		t.Fatalf("IngestStreamMaxLen() with invalid value = %d, want 0", got)
	}
}

func TestBuildXAddArgsWithoutTrim(t *testing.T) {
	t.Setenv("REPORT_INGEST_STREAM", "stream:test")
	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "0")

	args := buildXAddArgs("hello")
	if args.Stream != "stream:test" {
		t.Fatalf("buildXAddArgs().Stream = %q", args.Stream)
	}
	if args.MaxLen != 0 {
		t.Fatalf("buildXAddArgs().MaxLen = %d, want 0", args.MaxLen)
	}
	if args.Approx {
		t.Fatal("buildXAddArgs().Approx = true, want false")
	}
	values, ok := args.Values.(map[string]interface{})
	if !ok {
		t.Fatalf("buildXAddArgs().Values type = %T, want map[string]interface{}", args.Values)
	}
	if got := values["payload"]; got != "hello" {
		t.Fatalf("buildXAddArgs().Values[payload] = %#v", got)
	}
}

func TestBuildXAddArgsWithTrim(t *testing.T) {
	t.Setenv("REPORT_INGEST_STREAM_MAXLEN", "123")

	args := buildXAddArgs("hello")
	if args.MaxLen != 123 {
		t.Fatalf("buildXAddArgs().MaxLen = %d, want 123", args.MaxLen)
	}
	if !args.Approx {
		t.Fatal("buildXAddArgs().Approx = false, want true")
	}
	if _, ok := interface{}(args).(*redis.XAddArgs); !ok {
		t.Fatal("buildXAddArgs() should return *redis.XAddArgs")
	}
}

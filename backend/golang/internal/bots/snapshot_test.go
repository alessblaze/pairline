package bots

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewRedisSnapshotSyncerNilSafe(t *testing.T) {
	if got := NewRedisSnapshotSyncer(nil); got != nil {
		t.Fatalf("NewRedisSnapshotSyncer(nil) = %#v, want nil", got)
	}
}

func TestIsDuplicateKeyError(t *testing.T) {
	if !isDuplicateKeyError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("isDuplicateKeyError() should recognize Postgres duplicate key errors")
	}

	if isDuplicateKeyError(&pgconn.PgError{Code: "22001"}) {
		t.Fatal("isDuplicateKeyError() should reject non-duplicate Postgres errors")
	}

	if isDuplicateKeyError(nil) {
		t.Fatal("isDuplicateKeyError(nil) should be false")
	}
}

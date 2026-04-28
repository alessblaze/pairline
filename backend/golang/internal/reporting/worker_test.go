package reporting

import (
	"testing"
	"time"
)

func TestReportCreatedAtUsesCurrentTimeForZeroOrNegativeTimestamps(t *testing.T) {
	before := time.Now().UTC()

	zeroTime := reportCreatedAt(0)
	negativeTime := reportCreatedAt(-1)

	after := time.Now().UTC()

	if zeroTime.Before(before) || zeroTime.After(after) {
		t.Fatalf("reportCreatedAt(0) = %v, want current time window", zeroTime)
	}

	if negativeTime.Before(before) || negativeTime.After(after) {
		t.Fatalf("reportCreatedAt(-1) = %v, want current time window", negativeTime)
	}
}

func TestReportCreatedAtUsesProvidedTimestamp(t *testing.T) {
	got := reportCreatedAt(1_700_000_000_000)
	want := time.UnixMilli(1_700_000_000_000).UTC()
	if !got.Equal(want) {
		t.Fatalf("reportCreatedAt() = %v, want %v", got, want)
	}
}

func TestEnqueueAutoModerationWithNilWorkerOrAutoModeratorDoesNotPanic(t *testing.T) {
	var nilWorker *IngestWorker
	nilWorker.enqueueAutoModeration("report-1")

	worker := &IngestWorker{}
	worker.enqueueAutoModeration("report-1")
	worker.enqueueAutoModeration("")
}

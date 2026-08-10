package observability

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// TestDeleteRequestLogsBeforeBatches verifies the R2.4 batching end to end: a
// retention sweep far larger than one batch deletes every expired row in passes
// (3 passes at cleanupBatchSize for 12000 rows) while boundary/fresh rows
// survive.
func TestDeleteRequestLogsBeforeBatches(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)

	cutoff := time.Date(2026, 6, 30, 3, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	// 12000 expired rows inserted in one transaction so the test measures the
	// delete batching, not per-row Record transactions.
	insertRawRequestLogs(t, db, 12000, "bulk-expired", old)
	insertRawRequestLogs(t, db, 1, "bulk-fresh", cutoff.Add(time.Hour))

	deleted, err := repository.DeleteRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if deleted != 12000 {
		t.Fatalf("deleted = %d, want 12000", deleted)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&remaining); err != nil {
		t.Fatalf("count remaining logs: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining logs = %d, want 1 (the fresh row)", remaining)
	}
	var fresh int
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id = 'bulk-fresh-0'").Scan(&fresh); err != nil {
		t.Fatalf("count fresh row: %v", err)
	}
	if fresh != 1 {
		t.Fatal("fresh row was deleted by the retention sweep")
	}
}

// TestDeleteDailyStatsBeforeBatches verifies the aggregate sweep also batches:
// more rows than one pass are all removed, older than the boundary.
func TestDeleteDailyStatsBeforeBatches(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert count days all strictly before the cutoff: the oldest day is
	// cutoff-count and the newest is cutoff-1.
	insertRawDailyStats(t, db, 6000, cutoff.AddDate(0, 0, -6000))

	deleted, err := repository.DeleteDailyStatsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteDailyStatsBefore: %v", err)
	}
	if deleted != 6000 {
		t.Fatalf("deleted = %d, want 6000", deleted)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM daily_stats").Scan(&remaining); err != nil {
		t.Fatalf("count remaining stats: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining stats = %d, want 0", remaining)
	}
}

// TestDeleteBatchedStopsWhenBatchUnderflows locks in the loop semantics of the
// batching helper: it runs exactly one pass per full batch plus a final partial
// pass, and stops once a pass removes fewer rows than the batch.
func TestDeleteBatchedStopsWhenBatchUnderflows(t *testing.T) {
	remaining := int64(2500)
	calls := 0
	exec := func(_ context.Context, _ string, _ ...any) (sql.Result, error) {
		calls++
		count := remaining
		if count > 1000 {
			count = 1000
		}
		remaining -= count
		return fakeRowsResult{count: count}, nil
	}

	deleted, err := deleteBatched(context.Background(), exec, 1000, "DELETE ...", "2025-01-01")
	if err != nil {
		t.Fatalf("deleteBatched: %v", err)
	}
	if deleted != 2500 || calls != 3 || remaining != 0 {
		t.Fatalf("deleted=%d calls=%d remaining=%d, want deleted=2500 calls=3 remaining=0", deleted, calls, remaining)
	}
}

// TestDeleteBatchedReturnsCountOnError verifies partial progress is reported when
// a mid-sweep pass fails, so the cleanup worker logs how far it got.
func TestDeleteBatchedReturnsCountOnError(t *testing.T) {
	calls := 0
	exec := func(_ context.Context, _ string, _ ...any) (sql.Result, error) {
		calls++
		if calls == 2 {
			return nil, fmt.Errorf("writer busy")
		}
		return fakeRowsResult{count: 1000}, nil
	}
	deleted, err := deleteBatched(context.Background(), exec, 1000, "DELETE ...", "2025-01-01")
	if err == nil {
		t.Fatal("deleteBatched error = nil, want writer-busy error")
	}
	if deleted != 1000 {
		t.Fatalf("deleted = %d, want 1000 (progress before the failing pass)", deleted)
	}
}

type fakeRowsResult struct{ count int64 }

func (fakeRowsResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeRowsResult) RowsAffected() (int64, error) {
	return r.count, nil
}

func insertRawRequestLogs(t *testing.T, db *sql.DB, count int, prefix string, at time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin insert tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.Prepare(`
		INSERT INTO request_logs (request_id, endpoint, is_stream, http_status, outcome, duration_ms, attempt_count, created_at)
		VALUES (?, 'x', 0, 200, 'success', 1, 1, ?)`)
	if err != nil {
		t.Fatalf("prepare request log insert: %v", err)
	}
	defer func() { _ = statement.Close() }()
	created := formatTime(at)
	for index := range count {
		if _, err := statement.Exec(fmt.Sprintf("%s-%d", prefix, index), created); err != nil {
			t.Fatalf("insert request log %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit request log inserts: %v", err)
	}
}

func insertRawDailyStats(t *testing.T, db *sql.DB, count int, start time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin stats insert tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.Prepare(`
		INSERT INTO daily_stats (day, dimension_type, dimension_id) VALUES (?, 'global', 'all')`)
	if err != nil {
		t.Fatalf("prepare stats insert: %v", err)
	}
	defer func() { _ = statement.Close() }()
	for index := range count {
		day := start.AddDate(0, 0, index).UTC().Format("2006-01-02")
		if _, err := statement.Exec(day); err != nil {
			t.Fatalf("insert stats day %s: %v", day, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit stats inserts: %v", err)
	}
}

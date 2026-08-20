package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvidia-router/internal/clock"
)

// batchRecorderStub captures every batch flushed for assertions. It satisfies
// batchRecorder so BufferRecorder can be exercised without a real SQLite DB.
type batchRecorderStub struct {
	mu         sync.Mutex
	batches    [][]RequestRecord
	failRecord atomic.Bool
	processed  atomic.Int64
	flushed    chan []RequestRecord
	blocker    chan struct{}
}

func newBatchRecorderStub() *batchRecorderStub {
	return &batchRecorderStub{
		flushed: make(chan []RequestRecord, 64),
	}
}

func (s *batchRecorderStub) RecordBatch(_ context.Context, records []RequestRecord) error {
	if s.failRecord.Load() {
		return errors.New("stub: forced failure")
	}
	if s.blocker != nil {
		<-s.blocker // gate flush completion (simulates slow DB)
	}
	s.mu.Lock()
	cp := append([]RequestRecord(nil), records...)
	s.batches = append(s.batches, cp)
	s.mu.Unlock()
	s.processed.Add(int64(len(records)))
	select {
	case s.flushed <- cp:
	default:
	}
	return nil
}

func (s *batchRecorderStub) batchesSeen() [][]RequestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]RequestRecord, len(s.batches))
	copy(out, s.batches)
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBufferRecorderFlushesOnBatchSize(t *testing.T) {
	stub := newBatchRecorderStub()
	recorder := NewBufferRecorder(stub, clock.RealClock{}, BufferOptions{
		Capacity:   16,
		BatchSize:  3,
		FlushDelay: time.Hour, // only batch-size should fire
		Logger:     discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	<-recorder.started

	for index := 0; index < 3; index++ {
		if err := recorder.Record(context.Background(), RequestRecord{RequestID: "req-batch"}); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}

	select {
	case batch := <-stub.flushed:
		if len(batch) != 3 {
			t.Fatalf("flushed batch size = %d, want 3", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("batch-size flush did not fire")
	}

	if got := recorder.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
	cancel()
	<-done
}

func TestBufferRecorderFlushesOnTimer(t *testing.T) {
	stub := newBatchRecorderStub()
	recorder := NewBufferRecorder(stub, clock.RealClock{}, BufferOptions{
		Capacity:   64,
		BatchSize:  1024, // larger than any test volume, so only timer fires
		FlushDelay: 20 * time.Millisecond,
		Logger:     discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	<-recorder.started

	if err := recorder.Record(context.Background(), RequestRecord{RequestID: "req-timer"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	select {
	case batch := <-stub.flushed:
		if len(batch) != 1 || batch[0].RequestID != "req-timer" {
			t.Fatalf("flushed batch = %#v, want single req-timer", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("timer flush did not fire")
	}
	cancel()
	<-done
}

func TestBufferRecorderDropsOnFullBuffer(t *testing.T) {
	// The realistic back-pressure case: the flusher can't drain the channel
	// (slow/blocked DB), so records stack up in the bounded buffer and the
	// overflow path drops with a counter rather than blocking the caller.
	//
	// We never start the flusher goroutine here — that pins the buffer state to
	// pure channel capacity, so the drop count is deterministic.
	stub := &batchRecorderStub{flushed: make(chan []RequestRecord, 1)}
	recorder := NewBufferRecorder(stub, clock.RealClock{}, BufferOptions{
		Capacity:   2,
		BatchSize:  1024,
		FlushDelay: time.Hour,
		Logger:     discardLogger(),
	})

	// Enqueue more than capacity: exactly capacity records fit, the rest drop.
	for index := 0; index < 5; index++ {
		if err := recorder.Record(context.Background(), RequestRecord{RequestID: "overflow"}); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	if got := recorder.Dropped(); got < 3 { // 5 - capacity 2 = 3 dropped
		t.Fatalf("Dropped = %d, want >=3", got)
	}
}

func TestBufferRecorderDrainsOnContextCancel(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	recorder := NewBufferRecorder(repository, clock.RealClock{}, BufferOptions{
		Capacity:   64,
		BatchSize:  1024,
		FlushDelay: time.Hour, // forces drain to act as the only flush path
		Logger:     discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()

	// Enqueue several records, then cancel ctx — Run must drain them.
	for index := 0; index < 4; index++ {
		if err := recorder.Record(context.Background(), RequestRecord{
			RequestID: "drain-" + idChar(index),
			Endpoint:  "/v1/models", HTTPStatus: 200, Outcome: OutcomeSuccess,
			DurationMS: 1, AttemptCount: 1, CreatedAt: time.Date(2026, 7, 30, 0, index, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	cancel()
	<-done

	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id LIKE 'drain-%'").Scan(&count); err != nil {
		t.Fatalf("count drained logs: %v", err)
	}
	if count != 4 {
		t.Fatalf("drained rows = %d, want 4", count)
	}
}

func TestBufferRecorderStopTriggersDrain(t *testing.T) {
	stub := newBatchRecorderStub()
	recorder := NewBufferRecorder(stub, clock.RealClock{}, BufferOptions{
		Capacity:   32,
		BatchSize:  1024,
		FlushDelay: time.Hour,
		Logger:     discardLogger(),
	})

	done := make(chan struct{})
	go func() { recorder.Run(context.Background()); close(done) }()

	for index := 0; index < 2; index++ {
		if err := recorder.Record(context.Background(), RequestRecord{RequestID: "stop-" + idChar(index)}); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	recorder.Stop()
	<-done

	var total int64
	for _, batch := range stub.batchesSeen() {
		total += int64(len(batch))
	}
	if total != 2 {
		t.Fatalf("drained via Stop = %d, want 2", total)
	}
}

func TestBufferRecorderRecordBatchPersistsMultipleRecords(t *testing.T) {
	// Repository.RecordBatch is the flusher's persistence path; verify it writes
	// every record and all four dimensions per record in a single transaction.
	db := openObservabilityDB(t)
	accessKeyID := insertAccessKey(t, db)
	nvidiaKeyID := insertNVIDIAKey(t, db)
	records := []RequestRecord{
		{
			RequestID: "batch-1", Endpoint: "/v1/chat/completions", ModelID: "m1",
			AccessKeyID: &accessKeyID, NVIDIAKeyID: &nvidiaKeyID, HTTPStatus: 200,
			Outcome: OutcomeSuccess, DurationMS: 10, AttemptCount: 1,
			CreatedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			RequestID: "batch-2", Endpoint: "/v1/responses", ModelID: "m2",
			AccessKeyID: &accessKeyID, NVIDIAKeyID: &nvidiaKeyID, HTTPStatus: 500,
			Outcome: OutcomeFailure, DurationMS: 20, AttemptCount: 2,
			CreatedAt: time.Date(2026, 7, 30, 0, 1, 0, 0, time.UTC),
		},
	}
	if err := NewRepository(db).RecordBatch(context.Background(), records); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	var logCount int64
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id IN ('batch-1','batch-2')").Scan(&logCount); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 2 {
		t.Fatalf("log count = %d, want 2", logCount)
	}
	// global + m1 + m2 + nvidia_key + access_key = 5 dimensioned rows for the day
	var statRows int64
	if err := db.QueryRow("SELECT COUNT(*) FROM daily_stats WHERE day = '2026-07-30'").Scan(&statRows); err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if statRows != 5 {
		t.Fatalf("stat rows = %d, want 5", statRows)
	}
}

func TestRepositoryRecordBatchNullsDeletedForeignKeys(t *testing.T) {
	db := openObservabilityDB(t)
	accessKeyID := insertAccessKey(t, db)
	nvidiaKeyID := insertNVIDIAKey(t, db)
	if _, err := db.Exec("DELETE FROM access_keys WHERE id = ?", accessKeyID); err != nil {
		t.Fatalf("delete access key: %v", err)
	}
	if _, err := db.Exec("DELETE FROM nvidia_keys WHERE id = ?", nvidiaKeyID); err != nil {
		t.Fatalf("delete NVIDIA key: %v", err)
	}

	record := RequestRecord{
		RequestID: "deleted-foreign-keys", Endpoint: "/v1/chat/completions",
		AccessKeyID: &accessKeyID, NVIDIAKeyID: &nvidiaKeyID,
		HTTPStatus: 200, Outcome: OutcomeSuccess, DurationMS: 1, AttemptCount: 1,
		CreatedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
	}
	if err := NewRepository(db).RecordBatch(context.Background(), []RequestRecord{record}); err != nil {
		t.Fatalf("RecordBatch after foreign-key deletion: %v", err)
	}

	var nullReferences int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM request_logs
		WHERE request_id = ? AND access_key_id IS NULL AND nvidia_key_id IS NULL
	`, record.RequestID).Scan(&nullReferences); err != nil {
		t.Fatalf("count null foreign keys: %v", err)
	}
	if nullReferences != 1 {
		t.Fatalf("null foreign-key request rows = %d, want 1", nullReferences)
	}
}

func TestBufferRecorderEmptyBatchIsNoop(t *testing.T) {
	db := openObservabilityDB(t)
	if err := NewRepository(db).RecordBatch(context.Background(), nil); err != nil {
		t.Fatalf("RecordBatch(nil): %v", err)
	}
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestBufferRecorderForceFlushPersistsBufferedRecords(t *testing.T) {
	db := openObservabilityDB(t)
	repository := NewRepository(db)
	recorder := NewBufferRecorder(repository, clock.RealClock{}, BufferOptions{
		Capacity:   64,
		BatchSize:  1024, // large enough that nothing flushes without an explicit trigger
		FlushDelay: time.Hour,
		Logger:     discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	<-recorder.started

	for index := 0; index < 5; index++ {
		if err := recorder.Record(context.Background(), RequestRecord{
			RequestID: "force-" + idChar(index),
			Endpoint:  "/v1/models", HTTPStatus: 200, Outcome: OutcomeSuccess,
			DurationMS: 1, AttemptCount: 1, CreatedAt: time.Date(2026, 7, 30, 0, index, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("Record %d: %v", index, err)
		}
	}
	if err := recorder.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE request_id LIKE 'force-%'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Fatalf("ForceFlush persisted rows = %d, want 5", count)
	}
	cancel()
	<-done
}

func TestBufferRecorderForceFlushReturnsErrorFromBatch(t *testing.T) {
	stub := newBatchRecorderStub()
	stub.failRecord.Store(true)
	recorder := NewBufferRecorder(stub, clock.RealClock{}, BufferOptions{
		Capacity:   8,
		BatchSize:  1024,
		FlushDelay: time.Hour,
		Logger:     discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	<-recorder.started

	_ = recorder.Record(context.Background(), RequestRecord{RequestID: "fail"})
	if err := recorder.ForceFlush(context.Background()); err == nil {
		t.Fatal("ForceFlush error = nil, want forced failure")
	}
	cancel()
	<-done
}

func TestBufferRecorderForceFlushBeforeRunIsNoop(t *testing.T) {
	recorder := NewBufferRecorder(newBatchRecorderStub(), clock.RealClock{}, BufferOptions{Logger: discardLogger()})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := recorder.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush before Run: %v, want nil", err)
	}
}

func TestBufferRecorderForceFlushAfterStopIsNoop(t *testing.T) {
	recorder := NewBufferRecorder(newBatchRecorderStub(), clock.RealClock{}, BufferOptions{Logger: discardLogger()})
	done := make(chan struct{})
	go func() {
		recorder.Run(context.Background())
		close(done)
	}()
	recorder.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recorder did not stop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := recorder.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush after Stop: %v, want nil", err)
	}
}

// idChar maps 0..n to a single lowercase rune for unique-ish record IDs in tests.
func idChar(index int) string {
	return string(rune('a' + index))
}

// Compile-time guards: the stub and Repository both satisfy batchRecorder.
var _ batchRecorder = (*batchRecorderStub)(nil)
var _ batchRecorder = (*Repository)(nil)

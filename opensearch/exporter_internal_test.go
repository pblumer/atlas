package opensearch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/wal"
)

// capClient counts documents across batches without inspecting them.
type capClient struct{ total int }

func (c *capClient) Bulk(_ context.Context, _ string, items []Item) error {
	c.total += len(items)
	return nil
}

// TestExporterBatchCapDrainsOverTicks: with a small per-tick cap, a backlog larger
// than the cap is drained across several ticks — one tick never over-reads, and no
// record is skipped. This is an internal test because maxBatch is unexported.
func TestExporterBatchCapDrainsOverTicks(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer l.Close()

	const n = 5
	for pos := uint64(1); pos <= n; pos++ {
		rec := &model.Record{
			Header: model.RecordHeader{Position: pos, Key: pos, RecordType: model.RecordEvent, ValueType: model.VTProcessInstance, Intent: model.IntentTerminated, PartitionId: 1},
			Value:  &model.ProcessInstanceValue{ProcessDefKey: 1, State: model.PITerminated},
		}
		if err := l.Append(model.AppendRecord(nil, rec)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := l.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	client := &capClient{}
	e, err := New(filepath.Join(dir, "wal"), func() (uint64, error) { return n, nil }, client, "", NewPositionStore(filepath.Join(dir, "exporter")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.maxBatch = 2

	// First tick caps at 2.
	if got, err := e.Tick(context.Background()); err != nil || got != 2 {
		t.Fatalf("tick 1 = (%d, %v), want (2, nil)", got, err)
	}
	if e.hwm.Load() != 2 {
		t.Fatalf("hwm after tick 1 = %d, want 2", e.hwm.Load())
	}
	// Second tick another 2.
	if got, _ := e.Tick(context.Background()); got != 2 {
		t.Fatalf("tick 2 = %d, want 2", got)
	}
	// Third tick the remaining 1.
	if got, _ := e.Tick(context.Background()); got != 1 {
		t.Fatalf("tick 3 = %d, want 1", got)
	}
	if client.total != n {
		t.Fatalf("indexed %d docs total, want %d", client.total, n)
	}
	// Drained: a further tick does nothing.
	if got, _ := e.Tick(context.Background()); got != 0 {
		t.Fatalf("tick 4 = %d, want 0 (drained)", got)
	}
}

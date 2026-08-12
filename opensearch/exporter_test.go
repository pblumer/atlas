package opensearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/wal"
)

// fakeClient records the batches it was handed and can be armed to fail once.
type fakeClient struct {
	batches  [][]opensearch.Item
	failNext error // returned (and cleared) by the next Bulk when non-nil
}

func (f *fakeClient) Bulk(_ context.Context, _ string, items []opensearch.Item) error {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.batches = append(f.batches, append([]opensearch.Item(nil), items...))
	return nil
}

func (f *fakeClient) ids() []string {
	var out []string
	for _, b := range f.batches {
		for _, it := range b {
			out = append(out, it.ID)
		}
	}
	return out
}

// appendEvent writes one durable process-instance record at the given log position.
func appendEvent(t *testing.T, l *wal.Log, pos uint64, rt model.RecordType) {
	t.Helper()
	rec := &model.Record{
		Header: model.RecordHeader{
			Position:    pos,
			Key:         pos,
			Timestamp:   int64(pos) * 1_000_000,
			RecordType:  rt,
			ValueType:   model.VTProcessInstance,
			Intent:      model.IntentTerminated,
			PartitionId: 1,
		},
		Value: &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PITerminated, CreatedAt: 1, CompletedAt: 2},
	}
	if err := l.Append(model.AppendRecord(nil, rec)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func openLog(t *testing.T, dir string) *wal.Log {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	return l
}

func newExporter(t *testing.T, dir string, durable opensearch.DurableFunc, client opensearch.Client) *opensearch.Exporter {
	t.Helper()
	e, err := opensearch.New(filepath.Join(dir, "wal"), durable, client, "", opensearch.NewPositionStore(filepath.Join(dir, "exporter")))
	if err != nil {
		t.Fatalf("opensearch.New: %v", err)
	}
	return e
}

func wantIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d ids %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExporterIndexesUpToWatermark: records at or below the durable watermark are
// exported; records beyond it wait until the watermark advances.
func TestExporterIndexesUpToWatermark(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	for pos := uint64(1); pos <= 5; pos++ {
		appendEvent(t, l, pos, model.RecordEvent)
	}

	watermark := uint64(3)
	client := &fakeClient{}
	e := newExporter(t, dir, func() (uint64, error) { return watermark, nil }, client)

	n, err := e.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 3 {
		t.Fatalf("indexed %d, want 3 (up to the watermark)", n)
	}
	wantIDs(t, client.ids(), []string{"1", "2", "3"})
	if e.HighWaterMark() != 3 {
		t.Errorf("hwm = %d, want 3", e.HighWaterMark())
	}

	// The watermark advances; the rest export, none re-exported.
	watermark = 5
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	wantIDs(t, client.ids(), []string{"1", "2", "3", "4", "5"})

	// Idle tick with no new durable records does nothing.
	if n, _ := e.Tick(context.Background()); n != 0 {
		t.Errorf("idle Tick indexed %d, want 0", n)
	}
}

// TestExporterResumesAfterRestart: a new exporter over the same data dir loads the
// persisted high-water mark and does not re-export already-indexed records.
func TestExporterResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	for pos := uint64(1); pos <= 3; pos++ {
		appendEvent(t, l, pos, model.RecordEvent)
	}

	first := newExporter(t, dir, func() (uint64, error) { return 3, nil }, &fakeClient{})
	if _, err := first.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}

	// New records arrive; a fresh exporter (simulating a restart) resumes.
	for pos := uint64(4); pos <= 5; pos++ {
		appendEvent(t, l, pos, model.RecordEvent)
	}
	client := &fakeClient{}
	second := newExporter(t, dir, func() (uint64, error) { return 5, nil }, client)
	if second.HighWaterMark() != 3 {
		t.Fatalf("resumed hwm = %d, want 3 (loaded from disk)", second.HighWaterMark())
	}
	if _, err := second.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	wantIDs(t, client.ids(), []string{"4", "5"}) // 1..3 not re-exported
}

// TestExporterRetriesAfterBulkFailure: a bulk failure leaves the mark unadvanced,
// so the same records are retried (and not duplicated) on the next tick.
func TestExporterRetriesAfterBulkFailure(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	appendEvent(t, l, 1, model.RecordEvent)
	appendEvent(t, l, 2, model.RecordEvent)

	client := &fakeClient{failNext: errors.New("cluster down")}
	e := newExporter(t, dir, func() (uint64, error) { return 2, nil }, client)

	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatalf("Tick should surface the bulk failure")
	}
	if e.HighWaterMark() != 0 {
		t.Fatalf("hwm advanced to %d despite failure, want 0", e.HighWaterMark())
	}
	if len(client.batches) != 0 {
		t.Fatalf("no batch should have been recorded on failure")
	}

	// Recovered: the same records export, exactly once.
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatalf("retry Tick: %v", err)
	}
	wantIDs(t, client.ids(), []string{"1", "2"})
}

// TestExporterSkipsNonEventRecords: a non-event record advances the mark (it is
// consumed) but is never indexed — only facts are exported.
func TestExporterSkipsNonEventRecords(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	appendEvent(t, l, 1, model.RecordCommand) // not a persisted fact
	appendEvent(t, l, 2, model.RecordEvent)

	client := &fakeClient{}
	e := newExporter(t, dir, func() (uint64, error) { return 2, nil }, client)
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	wantIDs(t, client.ids(), []string{"2"})
	if e.HighWaterMark() != 2 {
		t.Errorf("hwm = %d, want 2 (advanced past the skipped record)", e.HighWaterMark())
	}
}

// TestExporterNewRejectsCorruptPosition: a corrupt persisted mark fails
// construction rather than silently restarting from genesis.
func TestExporterNewRejectsCorruptPosition(t *testing.T) {
	dir := t.TempDir()
	expDir := filepath.Join(dir, "exporter")
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expDir, "opensearch.pos"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := opensearch.New(filepath.Join(dir, "wal"), func() (uint64, error) { return 0, nil }, &fakeClient{}, "custom-index", opensearch.NewPositionStore(expDir))
	if err == nil {
		t.Fatalf("New should reject a corrupt position file")
	}
}

// TestExporterDurableError: an error reading the durable watermark aborts the tick
// without advancing the mark.
func TestExporterDurableError(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	appendEvent(t, l, 1, model.RecordEvent)

	e := newExporter(t, dir, func() (uint64, error) { return 0, errors.New("store closed") }, &fakeClient{})
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatalf("Tick should surface the durable-position error")
	}
	if e.HighWaterMark() != 0 {
		t.Errorf("hwm = %d, want 0", e.HighWaterMark())
	}
}

// TestExporterSavePositionError: an export that indexes successfully but cannot
// persist its high-water mark surfaces the error (the operator sees the exporter
// is not durably advancing).
func TestExporterSavePositionError(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	appendEvent(t, l, 1, model.RecordEvent)

	// Occupy the temp file path with a directory so Save's write fails — while the
	// real position file stays absent, so construction (which Loads it) still works.
	if err := os.MkdirAll(filepath.Join(dir, "exporter", "opensearch.pos.tmp"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := &fakeClient{}
	e := newExporter(t, dir, func() (uint64, error) { return 1, nil }, client)
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatalf("Tick should surface the position-save error")
	}
	// The document was still indexed (the failure is only in persisting the mark).
	wantIDs(t, client.ids(), []string{"1"})
}

// TestExporterDecodeErrorSurfaces: a frame that is CRC-valid in the log but not a
// decodable record aborts the tick (rather than silently skipping) so corruption
// is not swallowed.
func TestExporterDecodeErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	// A one-byte payload frames cleanly but is too short to be a record header.
	if err := l.Append([]byte{0xFF}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	e := newExporter(t, dir, func() (uint64, error) { return 1, nil }, &fakeClient{})
	if _, err := e.Tick(context.Background()); err == nil {
		t.Fatalf("Tick should surface a record decode error")
	}
}

// TestExporterDocumentShape: the exported document carries the header fields and
// the record value as JSON, keyed by log position.
func TestExporterDocumentShape(t *testing.T) {
	dir := t.TempDir()
	l := openLog(t, dir)
	defer l.Close()
	appendEvent(t, l, 1, model.RecordEvent)

	client := &fakeClient{}
	e := newExporter(t, dir, func() (uint64, error) { return 1, nil }, client)
	if _, err := e.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(client.batches) != 1 || len(client.batches[0]) != 1 {
		t.Fatalf("want one document, got %v", client.batches)
	}
	item := client.batches[0][0]
	if item.ID != "1" {
		t.Errorf("document id = %q, want 1", item.ID)
	}
	var doc struct {
		Position   uint64          `json:"position"`
		Partition  uint16          `json:"partition"`
		ValueType  string          `json:"valueType"`
		Intent     string          `json:"intent"`
		RecordType string          `json:"recordType"`
		Value      json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(item.Body, &doc); err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	if doc.Position != 1 || doc.Partition != 1 {
		t.Errorf("position/partition = %d/%d, want 1/1", doc.Position, doc.Partition)
	}
	if doc.ValueType != "ProcessInstance" {
		t.Errorf("valueType = %q, want ProcessInstance", doc.ValueType)
	}
	if doc.RecordType != "Event" {
		t.Errorf("recordType = %q, want Event", doc.RecordType)
	}
	if len(doc.Value) == 0 {
		t.Errorf("value payload is empty, want the marshalled record value")
	}
}

package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/wal"
)

// defaultMaxBatch bounds how many documents one Tick indexes, so a large backlog
// (first boot on a big log, or a long OpenSearch outage) drains over several
// ticks rather than one enormous _bulk request.
const defaultMaxBatch = 1000

// DurableFunc reports the highest log position that is durably committed — the
// state store's LastAppliedPosition. The exporter never indexes a record beyond
// it, which is how a WAL-tail honors durable-before-visible (invariant I2,
// ADR-0114): that watermark is written in the same committed batch as state, only
// after the WAL fsync, so any position at or below it is on disk.
type DurableFunc func() (uint64, error)

// Exporter tails the durable event log and bulk-indexes new records into
// OpenSearch. It runs off the processor goroutine and holds no engine state; it
// only reads committed WAL files and the durable-position watermark. It is not
// safe for concurrent use: drive it from a single goroutine (one Tick at a time).
type Exporter struct {
	tailer   *wal.Tailer
	durable  DurableFunc
	client   Client
	index    string
	pos      *PositionStore
	maxBatch int

	cursor wal.Cursor    // in-memory read position; re-derived from hwm on restart
	hwm    atomic.Uint64 // highest exported record position (persisted via pos)
}

// New constructs an Exporter tailing the WAL under walDir, bounded by durable,
// indexing into index (defaulting to [DefaultIndex]) through client, and
// persisting its high-water mark via pos. It loads the persisted mark so a
// restart resumes where the last run left off.
func New(walDir string, durable DurableFunc, client Client, index string, pos *PositionStore) (*Exporter, error) {
	hwm, err := pos.Load()
	if err != nil {
		return nil, err
	}
	if index == "" {
		index = DefaultIndex
	}
	e := &Exporter{
		tailer:   wal.NewTailer(walDir),
		durable:  durable,
		client:   client,
		index:    index,
		pos:      pos,
		maxBatch: defaultMaxBatch,
	}
	e.hwm.Store(hwm)
	return e, nil
}

// Tick runs one export pass: read newly-durable event records after the
// high-water mark (up to the durable watermark and the batch cap), bulk-index
// them, then advance and persist the mark. It returns the number of documents
// indexed. On any error the mark is left unchanged, so the same records are
// retried on the next Tick (idempotent by document id).
func (e *Exporter) Tick(ctx context.Context) (int, error) {
	watermark, err := e.durable()
	if err != nil {
		return 0, fmt.Errorf("opensearch: durable position: %w", err)
	}
	// hwm is only written here (Tick runs on one goroutine), but read concurrently by
	// the retention sweep (ADR-0115), so it is an atomic. Snapshot it once per Tick.
	hwm := e.hwm.Load()
	if watermark <= hwm {
		return 0, nil // nothing durable beyond what we have already exported
	}

	var items []Item
	lastConsumed := hwm
	next, err := e.tailer.Read(e.cursor, func(data []byte) (bool, error) {
		rec, err := model.ReadRecord(data)
		if err != nil {
			return false, fmt.Errorf("decode record: %w", err)
		}
		pos := rec.Header.Position
		if pos > watermark {
			return true, nil // beyond the durable bound: stop, leave for a later Tick
		}
		if len(items) >= e.maxBatch {
			return true, nil // batch full: resume at this record next Tick
		}
		if pos > hwm && rec.Header.RecordType == model.RecordEvent {
			item, err := toItem(rec)
			if err != nil {
				return false, err
			}
			items = append(items, item)
		}
		lastConsumed = pos
		return false, nil
	})
	if err != nil {
		return 0, fmt.Errorf("opensearch: read log: %w", err)
	}

	if len(items) > 0 {
		if err := e.client.Bulk(ctx, e.index, items); err != nil {
			return 0, err // mark not advanced → the batch is retried next Tick
		}
	}
	if lastConsumed > hwm {
		e.cursor = next
		e.hwm.Store(lastConsumed)
		if err := e.pos.Save(lastConsumed); err != nil {
			return len(items), err
		}
	}
	return len(items), nil
}

// HighWaterMark returns the highest record position exported so far — the value
// persisted across restarts. Safe to call from another goroutine (the retention
// sweep gates deletes on it, ADR-0115).
func (e *Exporter) HighWaterMark() uint64 { return e.hwm.Load() }

// document is the JSON shape of an exported event: the record header fields plus
// the record's value marshalled generically (inline, via the any field). It is a
// search/archival projection, not a curated per-value-type schema (ADR-0114).
type document struct {
	Position       uint64 `json:"position"`
	Partition      uint16 `json:"partition"`
	Key            uint64 `json:"key,omitempty"`
	SourcePosition uint64 `json:"sourcePosition,omitempty"`
	Timestamp      string `json:"timestamp"`
	RecordType     string `json:"recordType"`
	ValueType      string `json:"valueType"`
	Intent         string `json:"intent"`
	Value          any    `json:"value,omitempty"`
}

// toItem projects a record into an indexable document. The document id is the
// record's log position — globally unique and monotonic — so a re-indexed record
// overwrites rather than duplicates (at-least-once safety).
func toItem(rec model.Record) (Item, error) {
	h := rec.Header
	body, err := json.Marshal(document{
		Position:       h.Position,
		Partition:      h.PartitionId,
		Key:            h.Key,
		SourcePosition: h.SourcePos,
		Timestamp:      time.Unix(0, h.Timestamp).UTC().Format(time.RFC3339Nano),
		RecordType:     h.RecordType.String(),
		ValueType:      h.ValueType.String(),
		Intent:         h.Intent.String(),
		Value:          rec.Value,
	})
	if err != nil {
		return Item{}, fmt.Errorf("marshal document: %w", err)
	}
	return Item{ID: strconv.FormatUint(h.Position, 10), Body: body}, nil
}

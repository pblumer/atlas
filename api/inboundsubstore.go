package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inboundSubscription is an operator-configured inbound event binding for a clio
// connector (ADR-0075): the bridge watches WatchedSubject on the connector's clio
// instance and republishes each new event as an Atlas message named MessageName,
// correlated on CorrelationKey (a FEEL expression over the event body; empty =
// keyless), so the event both starts message-start processes and wakes waiting
// message-catch instances. LastEventID is a *best-effort* cursor advanced after each
// batch is durably published: it only speeds a restart's resume. Correctness — no
// duplicate delivery across a crash — is guaranteed by the engine's durable
// high-water mark (ADR-0075), not by this cursor, so losing it re-reads harmlessly.
type inboundSubscription struct {
	ID             string `json:"id"`
	ConnectorID    string `json:"connectorId"`    // FK → a kind:"clio" connector record
	WatchedSubject string `json:"watchedSubject"` // clio subject path, e.g. "orders/new"
	Recursive      bool   `json:"recursive"`      // include the subject's subtree
	MessageName    string `json:"messageName"`    // published Atlas message name
	CorrelationKey string `json:"correlationKey"` // FEEL over the event body; "" = keyless
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	LastEventID    string `json:"lastEventId,omitempty"` // best-effort resume cursor
	// StartFromTip makes a new subscription forward-only: on first activation the
	// bridge primes its cursor past the subject's existing backlog WITHOUT
	// republishing it, so connecting a watch to a subject that already has history
	// does not start a process per historical event (the reported /employees flood,
	// ADR-0075). Defaults to true on create; set false to intentionally backfill the
	// whole history. Primed records that the one-time skip has completed.
	StartFromTip bool `json:"startFromTip"`
	Primed       bool `json:"primed,omitempty"`
}

// inboundSubStore is a durable store for inbound subscriptions, one JSON file per id
// under a directory — the same sidecar approach as the connector store
// (ADR-0019/0041). It is owned solely by the server's run-loop goroutine, so it needs
// no locking, and it holds no secret material.
type inboundSubStore struct {
	dir string
}

// newInboundSubStore opens (creating if needed) the inbound-subscriptions directory.
func newInboundSubStore(dir string) (*inboundSubStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("inboundsubstore: create dir: %w", err)
	}
	return &inboundSubStore{dir: dir}, nil
}

func (s *inboundSubStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes a subscription durably (atomic write + directory fsync).
func (s *inboundSubStore) save(rec inboundSubscription) error {
	return atomicWriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

// get returns the subscription for an id, or ok=false if none exists.
func (s *inboundSubStore) get(id string) (inboundSubscription, bool, error) {
	data, err := os.ReadFile(s.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return inboundSubscription{}, false, nil
		}
		return inboundSubscription{}, false, fmt.Errorf("inboundsubstore: read: %w", err)
	}
	var rec inboundSubscription
	if err := json.Unmarshal(data, &rec); err != nil {
		return inboundSubscription{}, false, fmt.Errorf("inboundsubstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a subscription. A missing subscription is not an error.
func (s *inboundSubStore) delete(id string) error {
	if err := os.Remove(s.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inboundsubstore: remove: %w", err)
	}
	return fsyncDir(s.dir)
}

// loadAll reads every subscription, oldest first (creation order). Non-record files
// are ignored.
func (s *inboundSubStore) loadAll() ([]inboundSubscription, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("inboundsubstore: read dir: %w", err)
	}
	var out []inboundSubscription
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("inboundsubstore: read %s: %w", e.Name(), err)
		}
		var rec inboundSubscription
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("inboundsubstore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

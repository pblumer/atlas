package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// installedTemplate is a marketplace package a user has installed into this
// server (ADR-0081): the element-template payload (ADR-0027) plus the manifest
// fields the Modeler needs to render it in the palette. It is design-time data
// only — installing writes a template the compiler already accepts, never engine
// state — so, like every sidecar record, it holds no secret material (the "no
// secret travels in a shared artifact" rule of ADR-0081/0041/0069).
type installedTemplate struct {
	ID          string          `json:"id"`        // the installed record id (== package id)
	PackageID   string          `json:"packageId"` // the source marketplace package id
	Version     string          `json:"version"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Template    json.RawMessage `json:"template"`
	InstalledAt int64           `json:"installedAt"`
}

// marketplaceStore is a durable store for installed marketplace templates, one
// JSON file per id under a single directory — the same on-disk sidecar approach
// as the connector/project/draft stores (ADR-0019/0034/0041). Like them it is
// owned solely by the server's run-loop goroutine, so it needs no locking, and it
// holds no secret material.
type marketplaceStore struct {
	dir string
}

// newMarketplaceStore opens (creating if needed) the installed-templates directory.
func newMarketplaceStore(dir string) (*marketplaceStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("marketplacestore: create dir: %w", err)
	}
	return &marketplaceStore{dir: dir}, nil
}

func (s *marketplaceStore) fileFor(id string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(id))+".json")
}

// save writes an installed template durably (atomic write + directory fsync),
// overwriting any existing record with the same id (re-install / upgrade).
func (s *marketplaceStore) save(rec installedTemplate) error {
	return sidecar.WriteJSON(s.dir, s.fileFor(rec.ID), rec)
}

// get returns the installed template for an id, or ok=false if none exists.
func (s *marketplaceStore) get(id string) (installedTemplate, bool, error) {
	data, err := os.ReadFile(s.fileFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return installedTemplate{}, false, nil
		}
		return installedTemplate{}, false, fmt.Errorf("marketplacestore: read: %w", err)
	}
	var rec installedTemplate
	if err := json.Unmarshal(data, &rec); err != nil {
		return installedTemplate{}, false, fmt.Errorf("marketplacestore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes an installed template (uninstall). A missing record is not an
// error (idempotent).
func (s *marketplaceStore) delete(id string) error {
	if err := os.Remove(s.fileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("marketplacestore: remove: %w", err)
	}
	return sidecar.FsyncDir(s.dir)
}

// loadAll reads every installed template, oldest install first, so the Modeler
// lists them stably. Non-record files are ignored.
func (s *marketplaceStore) loadAll() ([]installedTemplate, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("marketplacestore: read dir: %w", err)
	}
	var out []installedTemplate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("marketplacestore: read %s: %w", e.Name(), err)
		}
		var rec installedTemplate
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("marketplacestore: decode %s: %w", e.Name(), err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InstalledAt != out[j].InstalledAt {
			return out[i].InstalledAt < out[j].InstalledAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

package api

import (
	"encoding/json"

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

// marketplaceStore is a durable store for installedTemplate records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type marketplaceStore = sidecar.Store[installedTemplate]

// newMarketplaceStore opens (creating if needed) the marketplace directory.
func newMarketplaceStore(dir string) (*marketplaceStore, error) {
	return sidecar.NewStore(dir, "marketplacestore",
		func(rec installedTemplate) string { return rec.ID },
		sidecar.Order(func(a, b installedTemplate) bool {
			if a.InstalledAt != b.InstalledAt {
				return a.InstalledAt < b.InstalledAt
			}
			return a.ID < b.ID
		}),
	)
}

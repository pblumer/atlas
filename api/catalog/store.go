package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pblumer/atlas/api/sidecar"
)

// Where the catalogue lives on disk.
//
// Three sidecar stores under one directory, each with the atomic-write and fsync
// discipline every other design-time store follows. Nothing here enters the event
// log or participates in recovery: a catalogue is authored, not executed.
//
// Catalogues and items are separate stores rather than one nested record because
// an item is referenced by several catalogues and edited through exactly one
// (its home), so nesting it under a catalogue would mean either copying it or
// picking an owner arbitrarily. Releases are separate again because they are
// immutable once written, where the other two are edited continuously.

// Store holds a catalogue's three kinds of record, and the one thing a catalogue
// owns that is not a record: its brand mark (see logo.go).
type Store struct {
	catalogs *sidecar.Store[Catalog]
	items    *sidecar.Store[Item]
	releases *sidecar.Store[Release]
	// logos is the directory the marks live in — image files rather than JSON, so
	// they are kept beside the stores rather than in one.
	logos string
}

// NewStore opens (creating if needed) the directories backing a catalogue.
func NewStore(dir string) (*Store, error) {
	catalogs, err := sidecar.NewStore(filepath.Join(dir, "catalogs"), "catalogstore",
		func(c Catalog) string { return c.ID },
		sidecar.Order(func(a, b Catalog) bool { return a.Rank < b.Rank }))
	if err != nil {
		return nil, err
	}
	items, err := sidecar.NewStore(filepath.Join(dir, "items"), "catalogitemstore",
		func(i Item) string { return i.ID },
		sidecar.Order(func(a, b Item) bool { return a.ID < b.ID }))
	if err != nil {
		return nil, err
	}
	// Newest first: the current release of a catalogue is what anybody asks for,
	// and a listing that buried it would be answered by scanning.
	releases, err := sidecar.NewStore(filepath.Join(dir, "releases"), "catalogreleasestore",
		func(r Release) string { return r.ID },
		sidecar.Order(func(a, b Release) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt > b.CreatedAt
			}
			return a.ID < b.ID
		}))
	if err != nil {
		return nil, err
	}
	logos := filepath.Join(dir, "logos")
	if err := os.MkdirAll(logos, 0o755); err != nil {
		return nil, fmt.Errorf("catalogstore: create logo dir: %w", err)
	}
	return &Store{catalogs: catalogs, items: items, releases: releases, logos: logos}, nil
}

// SaveCatalog writes a catalogue, replacing any record with the same id.
func (s *Store) SaveCatalog(c Catalog) error { return s.catalogs.Save(c) }

// Catalog reads one catalogue. A missing record is not an error: callers have to
// be able to tell "there is none" from "the store is broken".
func (s *Store) Catalog(id string) (Catalog, bool, error) { return s.catalogs.Get(id) }

// Catalogs lists every catalogue, lowest rank first.
func (s *Store) Catalogs() ([]Catalog, error) { return s.catalogs.LoadAll() }

// SaveItem writes an item, replacing any record with the same id.
func (s *Store) SaveItem(i Item) error { return s.items.Save(i) }

// Item reads one item.
func (s *Store) Item(id string) (Item, bool, error) { return s.items.Get(id) }

// Items lists every item, by id.
func (s *Store) Items() ([]Item, error) { return s.items.LoadAll() }

// SaveRelease writes a release. A release is never rewritten in practice — an
// order names one and its contents must not move — so this is the one call that
// creates it.
func (s *Store) SaveRelease(r Release) error { return s.releases.Save(r) }

// Release reads one release.
func (s *Store) Release(id string) (Release, bool, error) { return s.releases.Get(id) }

// ReleasesOf lists one catalogue's releases, newest first.
func (s *Store) ReleasesOf(catalogID string) ([]Release, error) {
	all, err := s.releases.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range all {
		if r.CatalogID == catalogID {
			out = append(out, r)
		}
	}
	return out, nil
}

// InputFor gathers what [Publish] validates for one catalogue: the catalogue
// itself, every item it offers, and the edges between them. Every catalogue is
// passed rather than only this one, because the rank-uniqueness check is about the
// set and not about any single record.
func (s *Store) InputFor(catalogID string, edges []Edge) (Input, bool, error) {
	cat, ok, err := s.Catalog(catalogID)
	if err != nil || !ok {
		return Input{}, ok, err
	}
	all, err := s.Catalogs()
	if err != nil {
		return Input{}, false, err
	}
	items, err := s.Items()
	if err != nil {
		return Input{}, false, err
	}

	offered := map[string]bool{}
	for _, id := range cat.Items {
		offered[id] = true
	}
	var carried []Item
	for _, it := range items {
		if offered[it.ID] {
			carried = append(carried, it)
		}
	}
	sort.Slice(carried, func(a, b int) bool { return carried[a].ID < carried[b].ID })

	return Input{Catalogs: all, Items: carried, Edges: edges}, true, nil
}

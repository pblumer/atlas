package api

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// form is a stored form definition: a form-js JSON schema plus the metadata the
// Tasks app and the Modeler list it by (ADR-0028). A user task binds a form by
// id (compiler: zeebe:formDefinition formId="..."); the Tasks app fetches the
// schema by that id and renders it. Schema is the raw form-js JSON, kept as a
// string so the store never has to understand the form model — rendering is a UI
// concern, storage is not.
type form struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the creator's user id, stamped on first save (ADR-0071). It
	// governs access only while the form is Ungrouped (the owner's personal space);
	// under a project, the project's scope governs. Empty (and open) on forms that
	// predate per-artifact ownership.
	OwnerID string `json:"ownerId,omitempty"`
	SavedAt int64  `json:"savedAt"`
	Schema  string `json:"schema"`
}

// formRevision is one schema a form has had, kept unchanged so an answer can be shown
// in the form it was given in (ADR-0554 §2). It is addressed by the form's id and
// the digest of the schema's bytes: saving the same schema twice is one revision.
type formRevision struct {
	Key     string `json:"key"` // formID + "@" + Digest
	FormID  string `json:"formId"`
	Digest  string `json:"digest"` // "sha256:<hex>"
	SavedAt int64  `json:"savedAt"`
	Schema  string `json:"schema"`
}

// formDigest is a schema's revision digest: SHA-256 over its bytes as stored.
func formDigest(schema string) string {
	sum := sha256.Sum256([]byte(schema))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func formRevisionKey(formID, digest string) string { return formID + "@" + digest }

// formStore is a durable store for form definitions, one JSON file per form id
// under a single directory. Like the draft store (ADR-0021) it reuses the on-disk
// sidecar approach of the deployment store (ADR-0019) and is owned solely by the
// server's run-loop goroutine, so it needs no locking of its own.
//
// Beside the forms it keeps every schema a form has been saved with, as an immutable
// revision in a sibling directory (ADR-0554 §2). Save and Delete keep the two
// in step, so every writer of a form — the Modeler, an application import, the system
// seed — records revisions without knowing about them.
type formStore struct {
	*sidecar.Store[form]
	revisions *sidecar.Store[formRevision]
}

// newFormStore opens (creating if needed) the forms directory and, beside it, the
// form-revisions directory. Forms list most recently saved first.
func newFormStore(dir string) (*formStore, error) {
	forms, err := sidecar.NewStore(dir, "formstore",
		func(rec form) string { return rec.ID },
		sidecar.Order(func(a, b form) bool { return a.SavedAt > b.SavedAt }),
	)
	if err != nil {
		return nil, err
	}
	revisions, err := sidecar.NewStore(filepath.Join(filepath.Dir(dir), "form-revisions"), "formrevisions",
		func(rec formRevision) string { return rec.Key },
	)
	if err != nil {
		return nil, err
	}
	return &formStore{Store: forms, revisions: revisions}, nil
}

// Save writes the form and records its schema as a revision, if it is not one already.
// The form first: a revision without its form would be kept for nothing, a form
// without its revision is recorded again by the next completion (RecordRevision).
func (s *formStore) Save(rec form) error {
	if err := s.Store.Save(rec); err != nil {
		return err
	}
	_, err := s.RecordRevision(rec)
	return err
}

// RecordRevision records rec's schema as a revision unless it is one already, and
// returns its digest. A completion calls it for the form the task was answered
// through, so a form saved before revisions existed has one once somebody answers it.
func (s *formStore) RecordRevision(rec form) (string, error) {
	digest := formDigest(rec.Schema)
	key := formRevisionKey(rec.ID, digest)
	if _, ok, err := s.revisions.Get(key); err != nil || ok {
		return digest, err
	}
	return digest, s.revisions.Save(formRevision{Key: key, FormID: rec.ID, Digest: digest, SavedAt: rec.SavedAt, Schema: rec.Schema})
}

// Revision is the form's revision with this digest, if it is kept.
func (s *formStore) Revision(formID, digest string) (formRevision, bool, error) {
	if formID == "" || !strings.HasPrefix(digest, "sha256:") {
		return formRevision{}, false, nil
	}
	return s.revisions.Get(formRevisionKey(formID, digest))
}

// Delete removes the form and every revision it had. A completed task answered through
// one of them is shown as a plain list of its answers from then on (ADR-0554 §2).
func (s *formStore) Delete(id string) error {
	if err := s.Store.Delete(id); err != nil {
		return err
	}
	all, err := s.revisions.LoadAll()
	if err != nil {
		return err
	}
	for _, r := range all {
		if r.FormID == id {
			if err := s.revisions.Delete(r.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

package decisiondoc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/api/token"
)

// Decision documentation (ADR-0324): a DMN decision
// published as one structured PDF — the decision requirements graph plus every
// decision's prose, inputs and rule table — so a reader outside Atlas can be
// handed the business rule itself.
//
// This file is a deliberate sibling of api/processdoc/store.go and agrees with it
// field for field where the two records overlap. The duplication is recorded in
// the ADR together with the rule for undoing it: when a third artifact kind wants
// a document, extract the generic half first and add the third second. Until then
// the two are meant to be diffed against each other.
//
// Design-time state. Producing a document puts nothing in the event log and never
// participates in replay; the per-decisionId version counter is rebuilt from these
// records at startup.

// Column is one column of a decision table, input or output, as the document
// reproduces it: what it reads (an input's expression, an output's name) and the
// type it declares.
type Column struct {
	// Label is the column heading a modeller gave it, empty when unnamed.
	Label string `json:"label,omitempty"`
	// Expression is what an input column evaluates, or an output column's variable
	// name. It is the thing a reader has to match a value against.
	Expression string `json:"expression,omitempty"`
	// Type is the declared typeRef, empty when the column declares none.
	Type string `json:"type,omitempty"`
}

// Rule is one row of a decision table: the unary tests in its input cells and the
// values in its output cells, in column order, plus whatever was written about it.
type Rule struct {
	// Inputs holds one entry per input column, in order. An empty entry is DMN's
	// "this column does not constrain this rule".
	Inputs []string `json:"inputs"`
	// Outputs holds one entry per output column, in order.
	Outputs []string `json:"outputs"`
	// Description is the rule's own annotation, which is where a modeller writes
	// why a rule exists.
	Description string `json:"description,omitempty"`
}

// Decision is one decision of the model as it was documented: the prose a reader
// needs about it and the logic it runs, snapshotted at publish time so a later
// edit to the model cannot rewrite what an already-published version says.
type Decision struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Description is the decision's <description> text.
	Description string `json:"description,omitempty"`
	// Inputs are the input data this decision consumes, by name and declared type.
	Inputs []Column `json:"inputs,omitempty"`
	// HitPolicy is the table's policy ("UNIQUE", "COLLECT" …), with its aggregation
	// where it has one. Empty for a decision whose logic is not a table.
	HitPolicy string `json:"hitPolicy,omitempty"`
	// InputColumns and OutputColumns describe the table's shape; Rules are its rows.
	InputColumns  []Column `json:"inputColumns,omitempty"`
	OutputColumns []Column `json:"outputColumns,omitempty"`
	Rules         []Rule   `json:"rules,omitempty"`
	// Literal is the FEEL expression of a decision whose logic is a literal
	// expression rather than a table — for those, this is the whole of the logic.
	Literal string `json:"literal,omitempty"`
}

// Doc is one published documentation version of a decision: immutable metadata
// describing what was documented, with the PDF stored beside it.
type Doc struct {
	ID string `json:"id"`
	// DecisionID is the DMN decision id the document describes. It binds to the id,
	// not a deployment, because a decision is documented whether or not it is
	// deployed — and a business rule is usually signed off before it runs, which is
	// the case this version line exists for.
	DecisionID string `json:"decisionId"`
	// ModelName is the DMN <definitions name>, and ModelRef the handle the decision
	// is stored under when it is in the model at all.
	ModelName string `json:"modelName,omitempty"`
	ModelRef  string `json:"modelRef,omitempty"`
	// Version is a per-decisionId counter, 1-based. It answers "which documented
	// state of this decision is that?", never "which deployment" — the deployment
	// version (ADR-0319) answers a different question and is deliberately separate.
	Version   int32  `json:"version"`
	Title     string `json:"title,omitempty"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
	PDFSize   int64  `json:"pdfSize"`
	// Decisions is the documented decision prose and logic, snapshotted by value.
	Decisions []Decision `json:"decisions,omitempty"`
	// XML is the DMN source the document was produced from, so a reader of the
	// history can recover the exact model a version describes.
	XML string `json:"xml,omitempty"`
	// ShareToken is the opaque handle that serves this version's PDF without a
	// login. Empty means unshared, which is the default — the artifact leaves the
	// system only when someone says so.
	ShareToken string `json:"shareToken,omitempty"`
}

// NewID mints a version id. 16 bytes of crypto randomness hex-encoded is
// filename-safe (so the id is its own store key) and collision-free in practice.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("decisiondocstore: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Store is a durable store for decision documentation versions: one JSON record
// per version id under a single directory, each beside the PDF it describes. The
// PDF is why it wraps the shared store rather than being one — a version is two
// files, and both have to go together.
//
// Every entry point rejects an id that is not bare hex before touching the
// filesystem. Ids here name their own files directly, so that check is what keeps
// an id from escaping the directory.
type Store struct {
	*sidecar.Store[Doc]
}

// NewStore opens (creating if needed) the decision-docs directory. Versions list
// grouped by decision, newest version first within each, tie-broken by id so the
// order is deterministic.
func NewStore(dir string) (*Store, error) {
	s, err := sidecar.NewStore(dir, "decisiondocstore",
		func(rec Doc) string { return rec.ID },
		sidecar.Names[Doc](func(id string) string { return id }, token.IsHex),
		sidecar.Order(func(a, b Doc) bool {
			if a.DecisionID != b.DecisionID {
				return a.DecisionID < b.DecisionID
			}
			if a.Version != b.Version {
				return a.Version > b.Version
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}

// pdfFileFor maps a version id to its document path, beside the record's own.
func (s *Store) pdfFileFor(id string) string {
	return filepath.Join(s.Dir(), id+".pdf")
}

// Save writes a version durably: the PDF first, then the record. The order
// matters — a record is the thing readers discover, so it must never point at a
// document that is not yet on disk (I2).
func (s *Store) Save(rec Doc, pdf []byte) error {
	if !token.IsHex(rec.ID) {
		return fmt.Errorf("decisiondocstore: refusing unsafe id %q", rec.ID)
	}
	if err := sidecar.WriteFile(s.Dir(), s.pdfFileFor(rec.ID), pdf); err != nil {
		return err
	}
	return s.SaveRecord(rec)
}

// SaveRecord rewrites only the metadata sidecar, leaving the stored PDF alone.
// Minting or revoking a share token is such an update: the document is immutable,
// who may read it is not.
func (s *Store) SaveRecord(rec Doc) error {
	if !token.IsHex(rec.ID) {
		return fmt.Errorf("decisiondocstore: refusing unsafe id %q", rec.ID)
	}
	return s.Store.Save(rec)
}

// Get returns a version's record, or ok=false if there is none. An unsafe id is a
// clean miss rather than a filesystem lookup. It shadows the embedded store's Get
// deliberately: every caller goes through the guard.
func (s *Store) Get(id string) (Doc, bool, error) {
	if !token.IsHex(id) {
		return Doc{}, false, nil
	}
	return s.Store.Get(id)
}

// PDF returns a version's document bytes. An unknown or unsafe id is an error
// rather than an empty document, so a handler cannot serve a zero-byte PDF as if
// it were real.
func (s *Store) PDF(id string) ([]byte, error) {
	if !token.IsHex(id) {
		return nil, fmt.Errorf("decisiondocstore: refusing unsafe id %q", id)
	}
	data, err := os.ReadFile(s.pdfFileFor(id))
	if err != nil {
		return nil, fmt.Errorf("decisiondocstore: read pdf: %w", err)
	}
	return data, nil
}

// ForDecision returns one decision's documentation history, newest version first.
func (s *Store) ForDecision(decisionID string) ([]Doc, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []Doc{}
	for _, rec := range all {
		if rec.DecisionID == decisionID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ByShareToken finds the version a public token addresses. An empty or unsafe
// token never matches, so the many unshared records — which carry no token — stay
// private.
func (s *Store) ByShareToken(shareToken string) (Doc, bool, error) {
	if !token.IsHex(shareToken) {
		return Doc{}, false, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return Doc{}, false, err
	}
	for _, rec := range all {
		if rec.ShareToken == shareToken {
			return rec, true, nil
		}
	}
	return Doc{}, false, nil
}

// PruneDecision enforces a retention limit on one decision's documentation
// history: it keeps the newest `keep` versions and deletes the rest, returning the
// ids it removed. Every version keeps a PDF, so an unpruned archive grows without
// limit.
//
// keep is clamped at zero — a negative limit would otherwise delete history it was
// asked to retain. keep >= the number of versions prunes nothing. Deleting the
// oldest is deliberate: history is answered newest-first, and the reason to prune
// is that ancient versions are the ones no longer worth their bytes.
func (s *Store) PruneDecision(decisionID string, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	versions, err := s.ForDecision(decisionID) // newest version first
	if err != nil {
		return nil, err
	}
	if len(versions) <= keep {
		return nil, nil
	}
	var pruned []string
	for _, rec := range versions[keep:] {
		if err := s.Delete(rec.ID); err != nil {
			return pruned, err
		}
		pruned = append(pruned, rec.ID)
	}
	return pruned, nil
}

// Delete removes a version and its document. A missing file is not an error, so
// cleanup is idempotent; the PDF goes with the record, since neither is meaningful
// without the other. It shadows the embedded store's Delete so no caller can
// remove a record and leave its document orphaned.
func (s *Store) Delete(id string) error {
	if !token.IsHex(id) {
		return nil
	}
	if err := os.Remove(s.pdfFileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("decisiondocstore: remove: %w", err)
	}
	return s.Store.Delete(id)
}

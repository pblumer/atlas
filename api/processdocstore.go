package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/sidecar"
)

// Process documentation (ADR-0143): a BPMN process published as one structured
// PDF — the diagram plus every element's documentation text and the annotations
// attached to it — so a reader outside Atlas can be handed the whole process.
//
// This is design-time state. Producing a document puts nothing in the event log
// and never participates in replay; the per-processId version counter is rebuilt
// from these records at startup, the discipline loadDeployments and
// loadReleaseVersions already follow.

// processDocCode is one code-bearing field of an element, snapshotted at publish
// time: a script task's job source (PowerShell/Python/JavaScript, ADR-0047), a
// FEEL condition or expression (ADR-0067), and the like. The document reproduces
// it verbatim so a reader can audit what a step actually runs, not merely what it
// is named — the prose says *what*, the code says *how*.
type processDocCode struct {
	// Label names the field the code came from, e.g. "Script" or "Condition".
	Label string `json:"label"`
	// Language is the code's language id (e.g. "powershell", "feel"), empty when
	// the field does not declare one.
	Language string `json:"language,omitempty"`
	// Source is the code itself, kept with its original whitespace so indentation
	// survives into the document.
	Source string `json:"source"`
}

// processDocElement is one BPMN element as it was documented: the prose a reader
// needs about it, snapshotted at publish time so a later edit to the model cannot
// rewrite what an already-published version says.
type processDocElement struct {
	ID   string `json:"id"`
	Type string `json:"type"` // the BPMN type, e.g. "bpmn:ServiceTask"
	Name string `json:"name,omitempty"`
	// Documentation is the element's <bpmn:documentation> text.
	Documentation string `json:"documentation,omitempty"`
	// Annotations are the <bpmn:textAnnotation> notes associated with this element,
	// in the order they were found. An annotation attached to nothing is documented
	// against the process itself rather than dropped.
	Annotations []string `json:"annotations,omitempty"`
	// Lane names the swimlane the element sits in, empty when the model has none
	// (ADR-0121).
	Lane string `json:"lane,omitempty"`
	// Code is the code-bearing fields of the element (scripts, FEEL expressions),
	// in the order a reader should meet them. Empty for the many elements that
	// carry no code.
	Code []processDocCode `json:"code,omitempty"`
}

// processDoc is one published documentation version of a process: immutable
// metadata describing what was documented, with the PDF stored beside it.
type processDoc struct {
	ID string `json:"id"`
	// ProcessID is the BPMN process id the document describes. It binds to the id,
	// not a deployment, because a process is documented whether or not it is
	// currently deployed.
	ProcessID   string `json:"processId"`
	ProcessName string `json:"processName,omitempty"`
	// Version is a per-processId counter, 1-based — the ADR-0128 layering above the
	// ADR-0019 per-process deployment version. It answers "which documented state of
	// this process is that?", not "which deployment".
	Version   int32  `json:"version"`
	Title     string `json:"title,omitempty"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
	// DeploymentKey and DeploymentVersion record which deployment was live when the
	// document was produced, zero when the model was documented from a draft.
	DeploymentKey     uint64 `json:"deploymentKey,omitempty"`
	DeploymentVersion int32  `json:"deploymentVersion,omitempty"`
	PDFSize           int64  `json:"pdfSize"`
	// Elements is the documented element prose, snapshotted by value.
	Elements []processDocElement `json:"elements,omitempty"`
	// XML is the BPMN source the document was produced from, so a reader of the
	// history can recover the exact model a version describes.
	XML string `json:"xml,omitempty"`
	// ShareToken is the opaque handle that serves this version's PDF without a
	// login (ADR-0029's mechanism). Empty means unshared, which is the default —
	// the artifact leaves the system only when someone says so.
	ShareToken string `json:"shareToken,omitempty"`
}

// newProcessDocID mints a version id. 16 bytes of crypto randomness hex-encoded
// is filename-safe (so the id is its own store key) and collision-free in
// practice.
func newProcessDocID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("processdocstore: random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// processDocStore is a durable store for process documentation versions
// (ADR-0143): one JSON record per version id under a single directory, each
// beside the PDF it describes. The PDF is why it wraps the shared store rather
// than being one — a version is two files, and both have to go together.
//
// Every entry point rejects an id that is not bare hex before touching the
// filesystem. Ids here name their own files directly (they are already
// filename-safe), so that check is what keeps an id from escaping the directory.
type processDocStore struct {
	*sidecar.Store[processDoc]
}

// newProcessDocStore opens (creating if needed) the process-docs directory.
// Versions list grouped by process, newest version first within each, tie-broken
// by id so the order is deterministic.
func newProcessDocStore(dir string) (*processDocStore, error) {
	s, err := sidecar.NewStore(dir, "processdocstore",
		func(rec processDoc) string { return rec.ID },
		sidecar.Names[processDoc](func(id string) string { return id }, isHexToken),
		sidecar.Order(func(a, b processDoc) bool {
			if a.ProcessID != b.ProcessID {
				return a.ProcessID < b.ProcessID
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
	return &processDocStore{s}, nil
}

// pdfFileFor maps a version id to its document path, beside the record's own.
func (s *processDocStore) pdfFileFor(id string) string {
	return filepath.Join(s.Dir(), id+".pdf")
}

// save writes a version durably: the PDF first, then the record. The order
// matters — a record is the thing readers discover, so it must never point at a
// document that is not yet on disk (I2).
func (s *processDocStore) save(rec processDoc, pdf []byte) error {
	if !isHexToken(rec.ID) {
		return fmt.Errorf("processdocstore: refusing unsafe id %q", rec.ID)
	}
	if err := sidecar.WriteFile(s.Dir(), s.pdfFileFor(rec.ID), pdf); err != nil {
		return err
	}
	return s.saveRecord(rec)
}

// saveRecord rewrites only the metadata sidecar, leaving the stored PDF alone.
// Minting or revoking a share token is such an update: the document is immutable,
// who may read it is not.
func (s *processDocStore) saveRecord(rec processDoc) error {
	if !isHexToken(rec.ID) {
		return fmt.Errorf("processdocstore: refusing unsafe id %q", rec.ID)
	}
	return s.Save(rec)
}

// get returns a version's record, or ok=false if there is none. An unsafe id is a
// clean miss rather than a filesystem lookup.
func (s *processDocStore) get(id string) (processDoc, bool, error) {
	if !isHexToken(id) {
		return processDoc{}, false, nil
	}
	return s.Get(id)
}

// pdf returns a version's document bytes. An unknown or unsafe id is an error
// rather than an empty document, so a handler cannot serve a zero-byte PDF as if
// it were real.
func (s *processDocStore) pdf(id string) ([]byte, error) {
	if !isHexToken(id) {
		return nil, fmt.Errorf("processdocstore: refusing unsafe id %q", id)
	}
	data, err := os.ReadFile(s.pdfFileFor(id))
	if err != nil {
		return nil, fmt.Errorf("processdocstore: read pdf: %w", err)
	}
	return data, nil
}

// forProcess returns one process's documentation history, newest version first.
func (s *processDocStore) forProcess(processID string) ([]processDoc, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []processDoc{}
	for _, rec := range all {
		if rec.ProcessID == processID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// byShareToken finds the version a public token addresses. An empty or unsafe
// token never matches, so the many unshared records — which carry no token — stay
// private.
func (s *processDocStore) byShareToken(token string) (processDoc, bool, error) {
	if !isHexToken(token) {
		return processDoc{}, false, nil
	}
	all, err := s.LoadAll()
	if err != nil {
		return processDoc{}, false, err
	}
	for _, rec := range all {
		if rec.ShareToken == token {
			return rec, true, nil
		}
	}
	return processDoc{}, false, nil
}

// pruneProcess enforces a retention limit on one process's documentation history:
// it keeps the newest `keep` versions and deletes the rest, returning the ids it
// removed. It is the bounded-growth follow-up ADR-0143 flagged: every version
// keeps a PDF, so an unpruned archive grows without limit.
//
// keep is clamped at zero — a negative limit would otherwise delete history it was
// asked to retain. keep >= the number of versions prunes nothing. Deleting the
// oldest is deliberate: history is answered newest-first, and the reason to prune
// is that ancient versions are the ones no longer worth their bytes.
func (s *processDocStore) pruneProcess(processID string, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	versions, err := s.forProcess(processID) // newest version first
	if err != nil {
		return nil, err
	}
	if len(versions) <= keep {
		return nil, nil
	}
	var pruned []string
	for _, rec := range versions[keep:] {
		if err := s.delete(rec.ID); err != nil {
			return pruned, err
		}
		pruned = append(pruned, rec.ID)
	}
	return pruned, nil
}

// delete removes a version and its document. A missing file is not an error, so
// cleanup is idempotent; the PDF goes with the record, since neither is meaningful
// without the other.
func (s *processDocStore) delete(id string) error {
	if !isHexToken(id) {
		return nil
	}
	if err := os.Remove(s.pdfFileFor(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("processdocstore: remove: %w", err)
	}
	return s.Delete(id)
}

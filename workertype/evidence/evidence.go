package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// EvidenceAPIVersion is the first evidence record format.
	EvidenceAPIVersion = "atlas.io/technical-capability-evidence/v1alpha1"
	// EvidenceKind identifies an evidence record.
	EvidenceKind = "TechnicalCapabilityEvidence"
	// ApprovalRepositoryProvenance is the approval rule of ADR-0483's initial phase:
	// the record reached protected main through a pull request the single maintainer
	// approved or merged. Recording it lets a stricter rule tell these records apart.
	ApprovalRepositoryProvenance = "repository-provenance/single-maintainer"
)

// The outcomes a case can have. A case the suite could not exercise says so rather
// than passing or being left out, so a record shows what was not checked.
const (
	OutcomePassed       = "passed"
	OutcomeFailed       = "failed"
	OutcomeNotExercised = "not-exercised"
)

const maxEvidenceBytes = 1 << 20

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Evidence is one record: which contract, which operation, what implements it, what
// was checked and with which outcome.
type Evidence struct {
	APIVersion     string         `json:"apiVersion"`
	Kind           string         `json:"kind"`
	Contract       ContractRef    `json:"contract"`
	Operation      OperationRef   `json:"operation"`
	Implementation Implementation `json:"implementation"`
	Conformance    Conformance    `json:"conformance"`
	Approval       string         `json:"approval"`
}

// ContractRef names the published contract file and the identity and digest of its
// bytes. The digest is the one value renewal never changes: a published contract does
// not change, and new bytes need a new version.
type ContractRef struct {
	Path    string `json:"path"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Profile string `json:"profile,omitempty"`
	Digest  string `json:"digest"`
}

// OperationRef names the Worker Type operation the evidence is about.
type OperationRef struct {
	WorkerType        string `json:"workerType"`
	WorkerTypeVersion string `json:"workerTypeVersion"`
	ID                string `json:"id"`
	JobType           string `json:"jobType"`
}

// Implementation is what runs the operation: the root packages of every path the
// Worker Type can run on and the dependency digest of their closure.
type Implementation struct {
	Roots  []string `json:"roots"`
	Digest string   `json:"digest"`
}

// Conformance is the suite that checked the operation, the digest of its code and
// fixtures, what it ran against, and every case's outcome.
type Conformance struct {
	Suite       string   `json:"suite"`
	Digest      string   `json:"digest"`
	Environment string   `json:"environment"`
	Results     []Result `json:"results"`
}

// Result is one case's outcome. Detail says why a case failed or was not exercised.
type Result struct {
	Case    string `json:"case"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// Binding is what a reviewer chooses when a record is created; the rest is derived.
type Binding struct {
	Contract   string
	WorkerType string
	Operation  string
	Suite      string
}

func (e Evidence) binding() Binding {
	return Binding{Contract: e.Contract.Path, WorkerType: e.Operation.WorkerType, Operation: e.Operation.ID, Suite: e.Conformance.Suite}
}

// Render is the one rendering of a record: two-space indentation, fields in format
// order, a final newline. Verify compares bytes, so a record is kept as Render writes it.
func (e Evidence) Render() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseEvidenceJSON decodes one complete record. Unknown fields, a second JSON value
// and missing or malformed values are refused; parsing does not check the record
// against the module, which is Verify's work.
func ParseEvidenceJSON(raw []byte) (Evidence, error) {
	if len(raw) > maxEvidenceBytes {
		return Evidence{}, fmt.Errorf("evidence record exceeds %d bytes", maxEvidenceBytes)
	}
	if !utf8.Valid(raw) {
		return Evidence{}, errors.New("evidence record is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var e Evidence
	if err := dec.Decode(&e); err != nil {
		return Evidence{}, fmt.Errorf("decode evidence record: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("decode evidence record: more than one JSON value")
	}
	if err := e.validate(); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

func (e Evidence) validate() error {
	if e.APIVersion != EvidenceAPIVersion {
		return fmt.Errorf("apiVersion must be %q", EvidenceAPIVersion)
	}
	if e.Kind != EvidenceKind {
		return fmt.Errorf("kind must be %q", EvidenceKind)
	}
	if e.Approval != ApprovalRepositoryProvenance {
		return fmt.Errorf("approval must be %q, the rule in force", ApprovalRepositoryProvenance)
	}
	for _, field := range []struct{ name, value string }{
		{"contract.path", e.Contract.Path},
		{"contract.id", e.Contract.ID},
		{"contract.version", e.Contract.Version},
		{"operation.workerType", e.Operation.WorkerType},
		{"operation.workerTypeVersion", e.Operation.WorkerTypeVersion},
		{"operation.id", e.Operation.ID},
		{"operation.jobType", e.Operation.JobType},
		{"conformance.suite", e.Conformance.Suite},
		{"conformance.environment", e.Conformance.Environment},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	for _, field := range []struct{ name, value string }{
		{"contract.digest", e.Contract.Digest},
		{"implementation.digest", e.Implementation.Digest},
		{"conformance.digest", e.Conformance.Digest},
	} {
		if !digestPattern.MatchString(field.value) {
			return fmt.Errorf("%s must be sha256:<64 lower-case hex digits>", field.name)
		}
	}
	if len(e.Implementation.Roots) == 0 || !slices.IsSorted(e.Implementation.Roots) || len(slices.Compact(slices.Clone(e.Implementation.Roots))) != len(e.Implementation.Roots) {
		return errors.New("implementation.roots must be a sorted list of distinct packages")
	}
	if len(e.Conformance.Results) == 0 {
		return errors.New("conformance.results must name at least one case")
	}
	seen := make(map[string]bool, len(e.Conformance.Results))
	for _, r := range e.Conformance.Results {
		if strings.TrimSpace(r.Case) == "" {
			return errors.New("conformance.results has a case without a name")
		}
		if seen[r.Case] {
			return fmt.Errorf("conformance.results names case %q twice", r.Case)
		}
		seen[r.Case] = true
		switch r.Outcome {
		case OutcomePassed, OutcomeFailed, OutcomeNotExercised:
		default:
			return fmt.Errorf("case %q has outcome %q, want %s, %s or %s", r.Case, r.Outcome, OutcomePassed, OutcomeFailed, OutcomeNotExercised)
		}
	}
	return nil
}

// contractPath checks that a record's contract is a published one: a clean,
// module-relative path inside the contracts directory.
func contractPath(p string) error {
	if p == "" || path.IsAbs(p) || strings.Contains(p, `\`) || path.Clean(p) != p {
		return fmt.Errorf("contract %q must be a clean, module-relative path with forward slashes", p)
	}
	if !strings.HasPrefix(p, contractsDir+"/") {
		return fmt.Errorf("contract %q is not under %s, where published contracts live", p, contractsDir)
	}
	return nil
}

package evidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/internal/impldigest"
	"github.com/pblumer/atlas/worker"
	"github.com/pblumer/atlas/workertype"
)

// publishedContractsDir is where published contracts live, relative to the module root.
const publishedContractsDir = "workertype/contracts"

// contractsDir is the directory a record's contract must be in. Tests point it at
// their fixtures, which are not published.
var contractsDir = publishedContractsDir

// computeDigest is impldigest.Compute; tests memoize it.
var computeDigest = impldigest.Compute

// The packages through which Atlas runs a built-in Worker Type's operations: a
// supervised worker through the worker package, the engine through api.
const (
	workerRoot = "github.com/pblumer/atlas/worker"
	engineRoot = "github.com/pblumer/atlas/api"
)

// runtimeRoots names the roots of every path a Worker Type of this kind can run on
// (ADR-0483, decided 2026-10-27). It reads the lists the runtime itself decides
// with: a kind the worker package serves can run in a supervised worker, and a kind
// that is not worker-only has an in-engine form.
func runtimeRoots(kind string) []string {
	var roots []string
	if !slices.Contains(api.DefaultSupervisedWorkerOnlyKinds(), kind) {
		roots = append(roots, engineRoot)
	}
	if slices.Contains(worker.KnownConnectorKinds(), kind) {
		roots = append(roots, workerRoot)
	}
	slices.Sort(roots)
	return roots
}

// catalogue is a Worker Type's own operation table and the job type its operations
// run as. Evidence reads the table the Worker Type runs from rather than a copy of
// it; a Worker Type gets an entry with its first record.
type catalogue struct {
	jobType string
	has     func(operation string) bool
}

var catalogues = map[string]catalogue{
	"entra": {jobType: compiler.EntraJobType, has: func(op string) bool { _, ok := entra.Ops[op]; return ok }},
}

// StaleError is a record that no longer matches the module: what it binds is still
// there, but something derived from it moved. Renewing it in the same change is the
// remedy ADR-0483 requires.
type StaleError struct {
	// Parts names what moved: "operation", "implementation", "conformance", or
	// "format" when only the rendering differs.
	Parts []string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("evidence record is stale (%s moved); renew it in this change with `go run ./workertype/evidence/cmd/evidence renew <record>`",
		strings.Join(e.Parts, ", "))
}

// Create derives a new record for a binding.
func Create(ctx context.Context, moduleDir string, b Binding) (Evidence, error) {
	return derive(ctx, moduleDir, b, "")
}

// Renew derives a record again from what it binds. It keeps the recorded contract
// digest: a published contract does not change, so contract bytes that moved are an
// error, not something renewal records.
func Renew(ctx context.Context, moduleDir string, rec Evidence) (Evidence, error) {
	fresh, err := derive(ctx, moduleDir, rec.binding(), rec.Contract.Digest)
	if err != nil {
		return Evidence{}, err
	}
	if fresh.Contract.ID != rec.Contract.ID || fresh.Contract.Version != rec.Contract.Version || fresh.Contract.Profile != rec.Contract.Profile {
		return Evidence{}, fmt.Errorf("the record names contract %s, but %s holds %s",
			contractName(rec.Contract), rec.Contract.Path, contractName(fresh.Contract))
	}
	return fresh, nil
}

func contractName(c ContractRef) string {
	name := c.ID + "@" + c.Version
	if c.Profile != "" {
		name += "+" + c.Profile
	}
	return name
}

func derive(ctx context.Context, moduleDir string, b Binding, contractDigest string) (Evidence, error) {
	if err := contractPath(b.Contract); err != nil {
		return Evidence{}, err
	}
	raw, err := os.ReadFile(filepath.Join(moduleDir, filepath.FromSlash(b.Contract)))
	if err != nil {
		return Evidence{}, err
	}
	digest, err := workertype.DigestTechnicalContractJSON(raw)
	if err != nil {
		return Evidence{}, fmt.Errorf("contract %s: %w", b.Contract, err)
	}
	if contractDigest != "" && digest != contractDigest {
		return Evidence{}, fmt.Errorf("contract %s changed after it was recorded (%s, now %s); a published contract does not change, so publish a new version instead",
			b.Contract, contractDigest, digest)
	}
	contract, err := workertype.ParseTechnicalContractJSON(raw)
	if err != nil {
		return Evidence{}, fmt.Errorf("contract %s: %w", b.Contract, err)
	}
	wt, ok := api.LookupBuiltInWorkerType(b.WorkerType)
	if !ok {
		return Evidence{}, fmt.Errorf("Worker Type %q is not one Atlas ships", b.WorkerType)
	}
	cat, ok := catalogues[wt.ConnectorKind]
	if !ok {
		return Evidence{}, fmt.Errorf("no operation catalogue is known for Worker Type %s; add its table to the catalogues", b.WorkerType)
	}
	if !cat.has(b.Operation) {
		return Evidence{}, fmt.Errorf("Worker Type %s has no operation %q", b.WorkerType, b.Operation)
	}
	suite, ok := suites[b.Suite]
	if !ok {
		return Evidence{}, fmt.Errorf("no conformance suite %q is registered", b.Suite)
	}
	roots := runtimeRoots(wt.ConnectorKind)
	impl, err := computeDigest(ctx, moduleDir, roots...)
	if err != nil {
		return Evidence{}, fmt.Errorf("implementation digest: %w", err)
	}
	suiteDigest, err := computeDigest(ctx, moduleDir, suite.Package)
	if err != nil {
		return Evidence{}, fmt.Errorf("suite %s digest: %w", b.Suite, err)
	}
	results, err := suite.Run(ctx, contract)
	if err != nil {
		return Evidence{}, fmt.Errorf("suite %s: %w", b.Suite, err)
	}
	rec := Evidence{
		APIVersion: EvidenceAPIVersion,
		Kind:       EvidenceKind,
		Contract: ContractRef{
			Path: b.Contract, ID: contract.Metadata.ID, Version: contract.Metadata.Version,
			Profile: contract.Metadata.Profile, Digest: digest,
		},
		Operation:      OperationRef{WorkerType: wt.ID, WorkerTypeVersion: wt.Version, ID: b.Operation, JobType: cat.jobType},
		Implementation: Implementation{Roots: roots, Digest: impl.Digest},
		Conformance: Conformance{
			Suite: b.Suite, Digest: suiteDigest.Digest, Environment: suite.Environment, Results: results,
		},
		Approval: ApprovalRepositoryProvenance,
	}
	// A suite that reports no case, a case twice or an outcome the format does not
	// know would produce a record that could never be read back.
	if err := rec.validate(); err != nil {
		return Evidence{}, fmt.Errorf("suite %s: %w", b.Suite, err)
	}
	return rec, nil
}

// Verify checks one record's bytes against the module. A record that cannot bind
// what it names is an error; one whose derived values moved is a [StaleError].
func Verify(ctx context.Context, moduleDir string, raw []byte) error {
	rec, err := ParseEvidenceJSON(raw)
	if err != nil {
		return err
	}
	fresh, err := Renew(ctx, moduleDir, rec)
	if err != nil {
		return err
	}
	want, err := fresh.Render()
	if err != nil {
		return err
	}
	if bytes.Equal(raw, want) {
		return nil
	}
	var parts []string
	if rec.Operation != fresh.Operation {
		parts = append(parts, "operation")
	}
	if !slices.Equal(rec.Implementation.Roots, fresh.Implementation.Roots) || rec.Implementation.Digest != fresh.Implementation.Digest {
		parts = append(parts, "implementation")
	}
	if rec.Conformance.Digest != fresh.Conformance.Digest || rec.Conformance.Environment != fresh.Conformance.Environment ||
		!slices.Equal(rec.Conformance.Results, fresh.Conformance.Results) {
		parts = append(parts, "conformance")
	}
	if len(parts) == 0 {
		parts = append(parts, "format")
	}
	return &StaleError{Parts: parts}
}

// VerifyAll checks every record under dir. The directory holds records only, and no
// two of them bind the same contract to the same operation; a directory that does not
// exist holds none. Every failing record is reported, each under its path.
func VerifyAll(ctx context.Context, moduleDir, dir string) error {
	var problems []error
	bound := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".json" {
			problems = append(problems, fmt.Errorf("%s: only evidence records (.json) belong here", p))
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := Verify(ctx, moduleDir, raw); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", p, err))
			return nil
		}
		rec, _ := ParseEvidenceJSON(raw)
		key := rec.Operation.WorkerType + " " + rec.Operation.ID + " " + contractName(rec.Contract)
		if other, ok := bound[key]; ok {
			problems = append(problems, fmt.Errorf("%s: binds %s, as %s already does", p, key, other))
			return nil
		}
		bound[key] = p
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

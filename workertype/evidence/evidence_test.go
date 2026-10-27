package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/internal/impldigest"
	"github.com/pblumer/atlas/workertype"
)

// The module root, seen from this package's directory.
const moduleDir = "../.."

const testContract = "workertype/evidence/testdata/contracts/account-create.json"

// fakeResults is what the fake suite reports; a test that changes it restores it.
var fakeResults = []Result{
	{Case: "creates an enabled account", Outcome: OutcomePassed},
	{Case: "an existing user name is ACCOUNT_ALREADY_EXISTS", Outcome: OutcomeFailed, Detail: "the operation reports a plain failure"},
	{Case: "a tenant without the permission is refused", Outcome: OutcomeNotExercised},
}

func TestMain(m *testing.M) {
	// Records in these tests bind contracts kept under testdata, which are not
	// published; the gate in workertype/evidence/gate binds the published ones.
	contractsDir = "workertype/evidence/testdata/contracts"
	Register("fake", Suite{
		Package:     "github.com/pblumer/atlas/internal/dirsync",
		Environment: "a fake that answers from a table; no provider",
		Run: func(context.Context, workertype.TechnicalContract) ([]Result, error) {
			return slices.Clone(fakeResults), nil
		},
	})
	Register("no-package", Suite{
		Package: "github.com/pblumer/atlas/no/such/package", Environment: "nothing",
		Run: func(context.Context, workertype.TechnicalContract) ([]Result, error) { return nil, nil },
	})
	Register("cannot-run", Suite{
		Package: "github.com/pblumer/atlas/internal/dirsync", Environment: "nothing",
		Run: func(context.Context, workertype.TechnicalContract) ([]Result, error) {
			return nil, errors.New("the fake did not start")
		},
	})
	Register("no-cases", Suite{
		Package: "github.com/pblumer/atlas/internal/dirsync", Environment: "nothing",
		Run: func(context.Context, workertype.TechnicalContract) ([]Result, error) { return nil, nil },
	})
	// The module does not change while the tests run, so each closure is listed once.
	var mu sync.Mutex
	seen := map[string]impldigest.Result{}
	compute := computeDigest
	computeDigest = func(ctx context.Context, dir string, roots ...string) (impldigest.Result, error) {
		key := dir + "\x00" + strings.Join(roots, "\x00")
		mu.Lock()
		defer mu.Unlock()
		if res, ok := seen[key]; ok {
			return res, nil
		}
		res, err := compute(ctx, dir, roots...)
		if err == nil {
			seen[key] = res
		}
		return res, err
	}
	os.Exit(m.Run())
}

func create(t *testing.T) Evidence {
	t.Helper()
	rec, err := Create(context.Background(), moduleDir, Binding{
		Contract: testContract, WorkerType: "atlas.entra", Operation: "create-user", Suite: "fake",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rec
}

func render(t *testing.T, rec Evidence) []byte {
	t.Helper()
	raw, err := rec.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return raw
}

// TestARecordIsCurrentWhenRenewingItChangesNothing: everything in a record but what
// it binds is derived from the module, and a record verifies exactly when deriving it
// again gives the same bytes.
func TestARecordIsCurrentWhenRenewingItChangesNothing(t *testing.T) {
	rec := create(t)
	raw, err := os.ReadFile(filepath.Join(moduleDir, testContract))
	if err != nil {
		t.Fatal(err)
	}
	contractDigest, err := workertype.DigestTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	impl, err := impldigest.Compute(context.Background(), moduleDir, "github.com/pblumer/atlas/worker")
	if err != nil {
		t.Fatal(err)
	}
	suite, err := impldigest.Compute(context.Background(), moduleDir, "github.com/pblumer/atlas/internal/dirsync")
	if err != nil {
		t.Fatal(err)
	}
	want := Evidence{
		APIVersion: EvidenceAPIVersion,
		Kind:       EvidenceKind,
		Contract: ContractRef{
			Path: testContract, ID: "io.atlas.identity.account.create", Version: "1.0.0",
			Profile: "identity-directory", Digest: contractDigest,
		},
		Operation: OperationRef{WorkerType: "atlas.entra", WorkerTypeVersion: "1.0.0", ID: "create-user", JobType: "io.atlas.entra"},
		Implementation: Implementation{
			Roots:  []string{"github.com/pblumer/atlas/worker"},
			Digest: impl.Digest,
		},
		Conformance: Conformance{
			Suite: "fake", Digest: suite.Digest,
			Environment: "a fake that answers from a table; no provider",
			Results:     fakeResults,
		},
		Approval: ApprovalRepositoryProvenance,
	}
	got, _ := json.MarshalIndent(rec, "", "  ")
	wanted, _ := json.MarshalIndent(want, "", "  ")
	if !bytes.Equal(got, wanted) {
		t.Fatalf("record =\n%s\nwant\n%s", got, wanted)
	}
	if err := Verify(context.Background(), moduleDir, render(t, rec)); err != nil {
		t.Fatalf("a record just created does not verify: %v", err)
	}
}

// TestVerifyNamesWhatMovedTheRecord: a record that no longer matches the module is
// stale, the error says which part moved and how to renew it, and renewing it gives
// a record that verifies.
func TestVerifyNamesWhatMovedTheRecord(t *testing.T) {
	fresh := create(t)
	for _, tc := range []struct {
		name   string
		part   string
		change func(*Evidence)
	}{
		{"implementation digest", "implementation", func(e *Evidence) { e.Implementation.Digest = otherDigest }},
		{"roots", "implementation", func(e *Evidence) {
			e.Implementation.Roots = []string{"github.com/pblumer/atlas/api", "github.com/pblumer/atlas/worker"}
		}},
		{"suite digest", "conformance", func(e *Evidence) { e.Conformance.Digest = otherDigest }},
		{"an outcome", "conformance", func(e *Evidence) { e.Conformance.Results[1].Outcome = OutcomePassed }},
		{"a detail", "conformance", func(e *Evidence) { e.Conformance.Results[1].Detail = "fine" }},
		{"environment", "conformance", func(e *Evidence) { e.Conformance.Environment = "a live tenant" }},
		{"job type", "operation", func(e *Evidence) { e.Operation.JobType = "io.atlas.entra.create" }},
		{"Worker Type version", "operation", func(e *Evidence) { e.Operation.WorkerTypeVersion = "1.0.1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := fresh
			rec.Implementation.Roots = slices.Clone(fresh.Implementation.Roots)
			rec.Conformance.Results = slices.Clone(fresh.Conformance.Results)
			tc.change(&rec)
			err := Verify(context.Background(), moduleDir, render(t, rec))
			var stale *StaleError
			if !errors.As(err, &stale) {
				t.Fatalf("Verify = %v, want a stale record", err)
			}
			if !slices.Contains(stale.Parts, tc.part) {
				t.Fatalf("stale parts = %v, want %q among them", stale.Parts, tc.part)
			}
			if !strings.Contains(err.Error(), "go run ./workertype/evidence/cmd/evidence renew") {
				t.Fatalf("the error does not say how to renew: %v", err)
			}
			renewed, err := Renew(context.Background(), moduleDir, rec)
			if err != nil {
				t.Fatalf("Renew: %v", err)
			}
			if err := Verify(context.Background(), moduleDir, render(t, renewed)); err != nil {
				t.Fatalf("the renewed record does not verify: %v", err)
			}
		})
	}
	t.Run("formatting", func(t *testing.T) {
		compact, err := json.Marshal(fresh)
		if err != nil {
			t.Fatal(err)
		}
		var stale *StaleError
		if err := Verify(context.Background(), moduleDir, compact); !errors.As(err, &stale) || !slices.Equal(stale.Parts, []string{"format"}) {
			t.Fatalf("Verify of a compact record = %v, want a stale format", err)
		}
	})
	t.Run("results the suite reports differently", func(t *testing.T) {
		saved := fakeResults
		t.Cleanup(func() { fakeResults = saved })
		fakeResults = slices.Clone(saved)
		fakeResults[0].Outcome = OutcomeFailed
		var stale *StaleError
		if err := Verify(context.Background(), moduleDir, render(t, fresh)); !errors.As(err, &stale) || !slices.Contains(stale.Parts, "conformance") {
			t.Fatalf("Verify = %v, want stale conformance results", err)
		}
	})
}

const otherDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// TestAChangedContractIsNotRenewed: a published contract never changes, so a record
// whose contract bytes moved is refused outright rather than reported as stale, and
// renewal does not paper over it.
func TestAChangedContractIsNotRenewed(t *testing.T) {
	rec := create(t)
	rec.Contract.Digest = otherDigest
	err := Verify(context.Background(), moduleDir, render(t, rec))
	var stale *StaleError
	if err == nil || errors.As(err, &stale) {
		t.Fatalf("Verify = %v, want a refusal that is not a stale record", err)
	}
	if !strings.Contains(err.Error(), "new version") {
		t.Fatalf("the refusal does not say to publish a new version: %v", err)
	}
	if _, err := Renew(context.Background(), moduleDir, rec); err == nil {
		t.Fatal("Renew accepted a changed contract")
	}
	rec = create(t)
	rec.Contract.ID = "io.atlas.identity.contact.create"
	if _, err := Renew(context.Background(), moduleDir, rec); err == nil {
		t.Fatal("Renew accepted a record naming another contract than its file holds")
	}
}

// TestCreateRefusesWhatItCannotBind: a record binds a contract kept where published
// contracts live, a Worker Type Atlas ships, an operation in that Worker Type's own
// catalogue, and a registered suite.
func TestCreateRefusesWhatItCannotBind(t *testing.T) {
	valid := Binding{Contract: testContract, WorkerType: "atlas.entra", Operation: "create-user", Suite: "fake"}
	for name, change := range map[string]func(*Binding){
		"an unknown Worker Type":               func(b *Binding) { b.WorkerType = "atlas.nope" },
		"a Worker Type with no catalogue":      func(b *Binding) { b.WorkerType = "atlas.mail" },
		"an operation outside the catalogue":   func(b *Binding) { b.Operation = "create-account" },
		"an unknown suite":                     func(b *Binding) { b.Suite = "nope" },
		"a contract outside the contracts dir": func(b *Binding) { b.Contract = "workertype/testdata/technical-account-create.json" },
		"a path out of the module":             func(b *Binding) { b.Contract = "workertype/evidence/testdata/contracts/../../../x.json" },
		"an absolute path":                     func(b *Binding) { b.Contract = "/etc/passwd" },
		"a missing contract":                   func(b *Binding) { b.Contract = "workertype/evidence/testdata/contracts/none.json" },
		"a contract that is not one":           func(b *Binding) { b.Contract = "workertype/evidence/testdata/contracts/not-a-contract.json" },
		"a contract with carriage returns":     func(b *Binding) { b.Contract = "workertype/evidence/testdata/contracts/crlf.json" },
		"a suite whose package is missing":     func(b *Binding) { b.Suite = "no-package" },
		"a suite that cannot run":              func(b *Binding) { b.Suite = "cannot-run" },
		"a suite that reports no case":         func(b *Binding) { b.Suite = "no-cases" },
	} {
		t.Run(name, func(t *testing.T) {
			b := valid
			change(&b)
			if rec, err := Create(context.Background(), moduleDir, b); err == nil {
				t.Fatalf("Create bound %+v", rec)
			}
		})
	}
}

// TestParseRefusesWhatIsNotARecord: a record on disk is complete and says nothing
// the format does not know.
func TestParseRefusesWhatIsNotARecord(t *testing.T) {
	valid := render(t, create(t))
	if _, err := ParseEvidenceJSON(valid); err != nil {
		t.Fatalf("a rendered record does not parse: %v", err)
	}
	edit := func(f func(map[string]any)) []byte {
		var doc map[string]any
		if err := json.Unmarshal(valid, &doc); err != nil {
			t.Fatal(err)
		}
		f(doc)
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	section := func(doc map[string]any, name string) map[string]any { return doc[name].(map[string]any) }
	for name, raw := range map[string][]byte{
		"an unknown field":   edit(func(d map[string]any) { d["reviewer"] = "alice" }),
		"a second value":     append(slices.Clone(valid), []byte("{}")...),
		"another apiVersion": edit(func(d map[string]any) { d["apiVersion"] = "atlas.io/v1" }),
		"another kind":       edit(func(d map[string]any) { d["kind"] = "WorkerType" }),
		"a malformed digest": edit(func(d map[string]any) { section(d, "implementation")["digest"] = "sha256:abc" }),
		"no roots":           edit(func(d map[string]any) { section(d, "implementation")["roots"] = []any{} }),
		"unsorted roots":     edit(func(d map[string]any) { section(d, "implementation")["roots"] = []any{"b", "a"} }),
		"no results":         edit(func(d map[string]any) { section(d, "conformance")["results"] = []any{} }),
		"a repeated case":    edit(func(d map[string]any) { r := section(d, "conformance")["results"].([]any); r[1] = r[0] }),
		"an unknown outcome": edit(func(d map[string]any) {
			section(d, "conformance")["results"].([]any)[0].(map[string]any)["outcome"] = "ok"
		}),
		"an unnamed case":       edit(func(d map[string]any) { section(d, "conformance")["results"].([]any)[0].(map[string]any)["case"] = " " }),
		"another approval rule": edit(func(d map[string]any) { d["approval"] = "reviewer: alice" }),
		"no operation":          edit(func(d map[string]any) { section(d, "operation")["id"] = "" }),
		"no suite":              edit(func(d map[string]any) { section(d, "conformance")["suite"] = "" }),
		"no contract path":      edit(func(d map[string]any) { section(d, "contract")["path"] = "" }),
		"invalid UTF-8":         bytes.Replace(valid, []byte("creates"), []byte("cr\xffates"), 1),
		"too large":             append(slices.Clone(valid), bytes.Repeat([]byte(" "), maxEvidenceBytes)...),
	} {
		t.Run(name, func(t *testing.T) {
			if rec, err := ParseEvidenceJSON(raw); err == nil {
				t.Fatalf("parsed %+v", rec)
			}
		})
	}
}

// TestASuiteNameIsRegisteredOnce: two suites under one name would let a record
// verify against a different suite than it renewed with.
func TestASuiteNameIsRegisteredOnce(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a second suite named fake was registered")
		}
	}()
	Register("fake", Suite{})
}

// TestTheRootsAreEveryPathAWorkerTypeRunsOn: decided 2026-10-27, variant A. A
// supervised worker runs a Worker Type through the worker package, the engine through
// api, and a record names every path its Worker Type can take. The paths are read
// from the same lists the runtime decides with, not kept here.
func TestTheRootsAreEveryPathAWorkerTypeRunsOn(t *testing.T) {
	for kind, want := range map[string][]string{
		"entra":       {"github.com/pblumer/atlas/worker"},
		"mail":        {"github.com/pblumer/atlas/api", "github.com/pblumer/atlas/worker"},
		"cloudevents": {"github.com/pblumer/atlas/api"},
	} {
		if got := runtimeRoots(kind); !slices.Equal(got, want) {
			t.Errorf("runtimeRoots(%q) = %v, want %v", kind, got, want)
		}
	}
	for _, kind := range api.DefaultSupervisedWorkerOnlyKinds() {
		if got := runtimeRoots(kind); !slices.Equal(got, []string{workerRoot}) {
			t.Errorf("worker-only %q has roots %v, want the worker package alone", kind, got)
		}
	}
	for _, kind := range api.DefaultOffloadedKinds() {
		if got := runtimeRoots(kind); !slices.Equal(got, []string{engineRoot, workerRoot}) {
			t.Errorf("offloadable %q has roots %v, want both paths", kind, got)
		}
	}
}

// TestVerifyAllReadsEveryRecordOnce: a records directory holds JSON records only,
// each binding something no other record binds; a directory that does not exist
// holds none.
func TestVerifyAllReadsEveryRecordOnce(t *testing.T) {
	raw := render(t, create(t))
	write := func(t *testing.T, dir, name string, body []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := VerifyAll(ctx, moduleDir, filepath.Join(t.TempDir(), "none")); err != nil {
		t.Fatalf("a missing directory: %v", err)
	}
	t.Run("one record", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "entra/create-user.json", raw)
		if err := VerifyAll(ctx, moduleDir, dir); err != nil {
			t.Fatal(err)
		}
	})
	for name, files := range map[string]map[string][]byte{
		"two records for one binding":  {"a.json": raw, "b.json": raw},
		"a file that is not a record":  {"a.json": raw, "notes.txt": []byte("x")},
		"a record that does not parse": {"a.json": []byte("{")},
		"a stale record":               {"a.json": bytes.Replace(raw, []byte(`"passed"`), []byte(`"failed"`), 1)},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, body := range files {
				write(t, dir, file, body)
			}
			err := VerifyAll(ctx, moduleDir, dir)
			if err == nil {
				t.Fatal("VerifyAll accepted the directory")
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, "a.json")) && !strings.Contains(err.Error(), "b.json") && !strings.Contains(err.Error(), "notes.txt") {
				t.Fatalf("the error names no file: %v", err)
			}
		})
	}
}

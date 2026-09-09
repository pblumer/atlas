package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// ADR-0167's mandate — a connector kind is not released until it is in the Repository
// catalog — with a test behind it at last.
//
// The mandate was written in August and enforced by nothing, so the catalog fell behind
// by nine kinds without a single failing test. That is the failure mode these guards
// exist for, and it is the same one the ADR-index guard and the OpenAPI drift test were
// written for: a convention the repository refuses to let rot.
//
// The guards are deliberately of two different strengths. Registration and citation are
// absolute — a kind with no row, or a row citing a record that does not exist, fails
// immediately. Package coverage is a ratchet, because sixteen packages do not exist yet
// and a test that cannot be switched on protects nothing.

// registryByJobType indexes the registry, failing on a duplicate row.
func registryByJobType(t *testing.T) map[string]releasedKind {
	t.Helper()
	byType := make(map[string]releasedKind, len(releasedKinds))
	for _, k := range releasedKinds {
		if prev, dup := byType[k.JobType]; dup {
			t.Errorf("job type %q has two rows in the registry (ADR %d and ADR %d) — one kind, one row",
				k.JobType, prev.ADR, k.ADR)
			continue
		}
		byType[k.JobType] = k
	}
	return byType
}

// TestEveryReservedJobTypeIsRegistered is the drift half, and the reason the registry is
// checked against the compiler rather than against a second hand-written list: the
// compiler's reserved slice is what *creates* a kind, so a kind cannot come into
// existence without this failing until somebody says what it is.
//
// The reverse direction matters as much. A row naming a job type the compiler does not
// reserve is a typo or a kind that was removed, and either way the registry is asserting
// something about nothing.
func TestEveryReservedJobTypeIsRegistered(t *testing.T) {
	byType := registryByJobType(t)
	reserved := compiler.ReservedJobTypes()

	for _, name := range reserved {
		if _, ok := byType[name]; !ok {
			t.Errorf("reserved job type %q has no row in releasedKinds — add one saying which ADR decided it, "+
				"what class it is, and which Repository package advertises it (ADR-0167)", name)
		}
	}
	inCompiler := make(map[string]bool, len(reserved))
	for _, name := range reserved {
		inCompiler[name] = true
	}
	for _, k := range releasedKinds {
		if !inCompiler[k.JobType] {
			t.Errorf("releasedKinds has a row for %q, which compiler.ReservedJobTypes does not contain — "+
				"a renamed or removed kind leaves the registry asserting something about nothing", k.JobType)
		}
	}
	if len(releasedKinds) != len(reserved) {
		t.Errorf("registry has %d rows for %d reserved job types", len(releasedKinds), len(reserved))
	}
}

// TestReleasedKindsCiteAnAcceptedRecord holds the release condition ADR-0167 actually
// states: a kind is released when it is authorable *and* its record is Accepted. A
// Proposed record means the kind is not released yet and owes nothing — which is only a
// meaningful exemption if the citation is real, so the record has to exist and its
// status has to be read rather than assumed.
func TestReleasedKindsCiteAnAcceptedRecord(t *testing.T) {
	status := adrStatuses(t)
	for _, k := range releasedKinds {
		st, ok := status[k.ADR]
		if !ok {
			t.Errorf("%s cites ADR-%04d, which is not a record in docs/adr", k.JobType, k.ADR)
			continue
		}
		if st != "Accepted" {
			t.Errorf("%s cites ADR-%04d, whose status is %q. A kind that ships is a decision that was taken; "+
				"either the record is out of date or this row cites the wrong one", k.JobType, k.ADR, st)
		}
	}
}

// TestExemptKindsSayWhy keeps an exemption from being the cheap way out. classEngine is
// the escape hatch in this registry — it means "no gallery entry could describe this" —
// and an escape hatch with no reason written next to it is indistinguishable from a row
// somebody could not be bothered to classify.
//
// Script tasks need no reason: ADR-0167 excludes the whole class in one sentence, for
// one reason, and repeating it per row would be noise.
func TestExemptKindsSayWhy(t *testing.T) {
	for _, k := range releasedKinds {
		if k.Class == classEngine && strings.TrimSpace(k.Why) == "" {
			t.Errorf("%s is exempt as classEngine but says no why — an exemption nobody wrote a reason for "+
				"is an oversight that looks like a decision", k.JobType)
		}
		if k.Class != classEngine && k.Why != "" {
			t.Errorf("%s carries a why but is not exempt; the field is for exemptions", k.JobType)
		}
		if k.Class == classScriptTask && k.Package != "" {
			t.Errorf("%s is a script task with package %q. ADR-0081's trust split keeps code-bearing "+
				"artifacts out of the gallery; publishing one runs against it", k.JobType, k.Package)
		}
	}
}

// TestRegisteredPackagesAreBundled checks the half of the mandate that can be absolute:
// a row that *claims* a package must name one that ships. Without this the registry
// could go green while every id in it pointed at a package renamed two releases ago,
// which is worse than an empty field — an empty field is honest.
func TestRegisteredPackagesAreBundled(t *testing.T) {
	bundled := bundledPackageIDs(t)
	for _, k := range releasedKinds {
		if k.Package == "" {
			continue
		}
		if !bundled[k.Package] {
			t.Errorf("%s claims Repository package %q, which is not bundled in api/repository_catalog. "+
				"Either the package was renamed and this row was not, or it was never added", k.JobType, k.Package)
		}
	}
}

// TestPackageCoverageOnlyImproves is the ratchet, and the compromise this whole file
// rests on.
//
// The honest state today is that sixteen released connector kinds have no package.
// Enforcing the mandate outright would mean a red build until all sixteen exist, and
// packages cannot be written responsibly in bulk: a package carries a kind's property
// schema, and a wrong schema produces a task the engine ignores — worse than no package
// at all, which is at least visibly absent.
//
// So the number is pinned instead. It may fall and it may not rise, which makes the two
// things that matter both impossible: a new kind cannot ship without a package while
// leaving the count alone, and the sixteen cannot be quietly forgotten, because the
// count is in the source with a test on it.
func TestPackageCoverageOnlyImproves(t *testing.T) {
	var missing []string
	for _, k := range releasedKinds {
		if k.Class == classConnector && k.Package == "" {
			missing = append(missing, k.JobType)
		}
	}
	switch {
	case len(missing) > packagesOwed:
		t.Errorf("%d released connector kinds have no Repository package, up from the pinned %d:\n  %s\n"+
			"A released kind ships with its gallery package — that is one unit of work, not two (ADR-0167).",
			len(missing), packagesOwed, strings.Join(missing, "\n  "))
	case len(missing) < packagesOwed:
		t.Errorf("only %d released connector kinds now lack a package, and the pinned figure is still %d. "+
			"Lower packagesOwed to %d so the ratchet holds the ground that was gained.",
			len(missing), packagesOwed, len(missing))
	}
}

// bundledPackageIDs reads the ids of the packages that actually ship.
func bundledPackageIDs(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir("repository_catalog")
	if err != nil {
		t.Fatalf("read repository_catalog: %v", err)
	}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("repository_catalog", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var pkg struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &pkg); err != nil {
			t.Fatalf("decode %s: %v", e.Name(), err)
		}
		if pkg.ID == "" {
			t.Errorf("%s has no id", e.Name())
			continue
		}
		ids[pkg.ID] = true
	}
	if len(ids) == 0 {
		t.Fatal("no bundled packages found; this guard would pass vacuously")
	}
	return ids
}

var adrFileName = regexp.MustCompile(`^(\d{4})-[a-z0-9-]+\.md$`)

// adrStatuses reads each record's declared status, keyed by number. It parses the
// records rather than the index because the index is generated from them, and a guard
// that reads the derived copy cannot catch the two disagreeing — that is what
// docs/adr's own tests are for.
func adrStatuses(t *testing.T) map[int]string {
	t.Helper()
	dir := filepath.Join("..", "docs", "adr")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read docs/adr: %v", err)
	}
	statusLine := regexp.MustCompile(`(?m)^- \*\*Status:\*\* (.+)$`)
	out := make(map[int]string, len(entries))
	for _, e := range entries {
		m := adrFileName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		s := statusLine.FindStringSubmatch(string(body))
		if s == nil {
			t.Errorf("%s has no status line", e.Name())
			continue
		}
		// "Accepted (amended …)" is Accepted; only the word itself is compared.
		st := strings.TrimSpace(s[1])
		if i := strings.Index(st, "("); i >= 0 {
			st = strings.TrimSpace(st[:i])
		}
		out[num] = st
	}
	if len(out) == 0 {
		t.Fatal("no ADR records found; this guard would pass vacuously")
	}
	return out
}

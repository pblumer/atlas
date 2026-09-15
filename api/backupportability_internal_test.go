package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The acceptance suite for
// ADR-draft-a-portable-backup-does-not-overwrite-another-installations-identity.
//
// Every case here is two installations, because that is the only instrument that can
// see the defect: one installation restoring its own archive was always fine, and is
// pinned below so it stays that way.

// portableBackupOf downloads an installation's design-time archive — the file
// ADR-0107 describes as the one an author moves between installations.
func portableBackupOf(t *testing.T, stack *decisionStack) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	stack.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// restoreReport is what the restore answers, including what it declined to take.
type restoreReport struct {
	Restored   int                `json:"restored"`
	Skipped    int                `json:"skipped"`
	Collisions []restoreCollision `json:"collisions"`
	Note       string             `json:"note"`
}

func restoreInto(t *testing.T, stack *decisionStack, archive []byte) restoreReport {
	t.Helper()
	rec := httptest.NewRecorder()
	stack.srv.Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/restore", bytes.NewReader(archive)))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	var out restoreReport
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode restore report: %v (%s)", err, rec.Body.String())
	}
	return out
}

func nodeIDOf(t *testing.T, stack *decisionStack) string {
	t.Helper()
	code, b := stack.x.do(http.MethodGet, "/api/v1/node", "")
	if code != http.StatusOK {
		t.Fatalf("node: %d %s", code, b)
	}
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("decode node: %v (%s)", err, b)
	}
	if d.ID == "" {
		t.Fatal("this installation has no node id, so the test cannot tell two apart")
	}
	return d.ID
}

// processesOf lists what an installation has deployed, by key.
func processesOf(t *testing.T, stack *decisionStack) map[uint64]string {
	t.Helper()
	code, b := stack.x.do(http.MethodGet, "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("processes: %d %s", code, b)
	}
	var rows []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, b)
	}
	out := map[uint64]string{}
	for _, r := range rows {
		out[r.Key] = r.ProcessID
	}
	return out
}

// TestARestoredDefinitionDoesNotInheritTheLocalHistory is the headline, and it is
// written as the corruption rather than as the rule.
//
// Measured before the fix: A's `alpha` landed on B's key 1, and answered with the one
// finished instance `beta` had run — with the visit re-labelled onto `alpha`'s own
// element, because the per-element aggregates are keyed by definition key and element
// index. A definition deployed on another box reported work it had never done.
func TestARestoredDefinitionDoesNotInheritTheLocalHistory(t *testing.T) {
	a := bootDecisionStack(t, t.TempDir())
	deployProcess(t, a.x, plainProcess("alpha"))
	archive := portableBackupOf(t, a)
	a.shutdown()

	dirB := t.TempDir()
	b := bootDecisionStack(t, dirB)
	keyB := deployProcess(t, b.x, runThroughProcess("beta"))
	if code, body := b.x.do(http.MethodPost, "/api/v1/instances", `{"processId":"beta"}`); code != http.StatusOK {
		t.Fatalf("start beta: %d %s", code, body)
	}
	before := runtimeOf(t, b.x, keyB)
	if before.Finished != 1 {
		t.Fatalf("beta finished = %d, want the 1 that makes the inheritance visible", before.Finished)
	}

	report := restoreInto(t, b, archive)
	if report.Skipped != 1 {
		t.Fatalf("skipped = %d (%+v), want the one definition whose key is taken here", report.Skipped, report.Collisions)
	}
	if got := report.Collisions[0]; got.Key != keyB || got.Here != "beta v1" || got.Incoming != "alpha v1" {
		t.Errorf("collision = %+v, want key %d holding beta v1 against alpha v1", got, keyB)
	}
	b.shutdown()

	rebooted := bootDecisionStack(t, dirB)
	defer rebooted.shutdown()
	if got := processesOf(t, rebooted); got[keyB] != "beta" {
		t.Fatalf("key %d is now %q; this installation's history is filed under it", keyB, got[keyB])
	}
	after := runtimeOf(t, rebooted.x, keyB)
	if after.Finished != before.Finished {
		t.Errorf("finished = %d after the restore, was %d", after.Finished, before.Finished)
	}
}

// TestAnInstallationKeepsItsIdentityThroughARestore: the archive carried
// settings/node.json, so restoring a colleague's models made this installation answer
// as theirs — two running engines with one id, and no way left to tell where the rest
// of the archive came from.
func TestAnInstallationKeepsItsIdentityThroughARestore(t *testing.T) {
	a := bootDecisionStack(t, t.TempDir())
	idA := nodeIDOf(t, a)
	archive := portableBackupOf(t, a)
	a.shutdown()

	dirB := t.TempDir()
	b := bootDecisionStack(t, dirB)
	idB := nodeIDOf(t, b)
	if idA == idB {
		t.Fatal("the two installations start with the same id; the test cannot tell them apart")
	}
	restoreInto(t, b, archive)
	b.shutdown()

	rebooted := bootDecisionStack(t, dirB)
	defer rebooted.shutdown()
	if got := nodeIDOf(t, rebooted); got != idB {
		t.Errorf("node id = %q after restoring another installation's archive, want its own %q (the archive's was %q)", got, idB, idA)
	}
}

// TestAnInstallationRestoringItsOwnBackupIsUnchanged pins what must keep working.
// ADR-0107 chose an overlay, and an operator restoring their own archive — including
// an older one — still gets one. The identity test is identity, not equality, exactly
// so this case is not caught by it.
func TestAnInstallationRestoringItsOwnBackupIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	a := bootDecisionStack(t, dir)
	key := deployProcess(t, a.x, plainProcess("alpha"))
	archive := portableBackupOf(t, a)

	report := restoreInto(t, a, archive)
	if report.Skipped != 0 {
		t.Fatalf("skipped = %d (%+v) restoring an installation's own archive", report.Skipped, report.Collisions)
	}
	if report.Restored == 0 {
		t.Error("nothing was restored; the overlay ADR-0107 chose has stopped working")
	}
	a.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	if got := processesOf(t, rebooted); got[key] != "alpha" {
		t.Errorf("key %d is %q after restoring its own archive, want alpha", key, got[key])
	}
}

// TestAFreeKeyIsRestoredNormally: the rule is about a key that is taken, not about
// keys. An archive whose definitions land on keys this installation has never issued
// is taken in full — the fresh-instance migration ADR-0107 is for.
func TestAFreeKeyIsRestoredNormally(t *testing.T) {
	a := bootDecisionStack(t, t.TempDir())
	deployProcess(t, a.x, plainProcess("alpha"))
	deployProcess(t, a.x, plainProcess("omega"))
	archive := portableBackupOf(t, a)
	a.shutdown()

	dirB := t.TempDir()
	b := bootDecisionStack(t, dirB)
	report := restoreInto(t, b, archive)
	if report.Skipped != 0 {
		t.Fatalf("skipped = %d (%+v) onto an installation that has deployed nothing", report.Skipped, report.Collisions)
	}
	b.shutdown()

	rebooted := bootDecisionStack(t, dirB)
	defer rebooted.shutdown()
	got := processesOf(t, rebooted)
	if len(got) != 2 {
		t.Fatalf("processes = %v, want both of the archive's definitions", got)
	}
}

// TestADecisionDeploymentIsHeldBackTheSameWay: decision deployments draw from the
// same counter and are pinned by key (ADR-0327), so a foreign one arriving on a taken
// key breaks every local pin to it. Same rule, named by what a pin means.
func TestADecisionDeploymentIsHeldBackTheSameWay(t *testing.T) {
	a := bootDecisionStack(t, t.TempDir())
	deployOneDecision(t, a.x, "", eligibilityDMN("approve"))
	archive := portableBackupOf(t, a)
	a.shutdown()

	b := bootDecisionStack(t, t.TempDir())
	defer b.shutdown()
	mine := deployOneDecision(t, b.x, "", discountDMN)

	report := restoreInto(t, b, archive)
	if report.Skipped != 1 {
		t.Fatalf("skipped = %d (%+v), want the decision deployment whose key is taken here", report.Skipped, report.Collisions)
	}
	if got := report.Collisions[0]; got.Kind != "decision" || got.Key != mine.Key {
		t.Errorf("collision = %+v, want a decision clash on key %d", got, mine.Key)
	}
	if !contains(report.Note, "does not travel") {
		t.Errorf("note = %q, want it to say why the records were held back", report.Note)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

var _ = fmt.Sprint

// TestTheWholeInstanceSnapshotStillCarriesTheIdentity is the other half of the
// decision, and it is a guard rather than a fix: the two archives are for different
// things, and applying the portable rule to the snapshot would quietly break the case
// ADR-0109 exists for — reconstituting *this* engine on another box, node id and all.
func TestTheWholeInstanceSnapshotStillCarriesTheIdentity(t *testing.T) {
	dir := t.TempDir()
	a := bootDecisionStack(t, dir)
	want := nodeIDOf(t, a)

	rec := httptest.NewRecorder()
	a.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backup/full", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot: %d %s", rec.Code, rec.Body.String())
	}
	a.shutdown()

	if !archiveHas(t, rec.Body.Bytes(), "settings/node.json", want) {
		t.Error("the whole-instance snapshot no longer carries the node identity; a restore elsewhere would come back as a different node")
	}
}

// TestThePortableBackupCarriesEverySettingButTheIdentity: the exclusion is one file,
// not the store. The theme, the claim mapping and the rest are an operator's settings
// and are exactly what an author moving to another installation wants.
func TestThePortableBackupCarriesEverySettingButTheIdentity(t *testing.T) {
	dir := t.TempDir()
	a := bootDecisionStack(t, dir)
	id := nodeIDOf(t, a)
	if code, b := a.x.do(http.MethodPut, "/api/v1/settings/theme", `{"accent":"#123456"}`); code != http.StatusOK {
		t.Fatalf("set theme: %d %s", code, b)
	}
	archive := portableBackupOf(t, a)
	a.shutdown()

	if archiveHas(t, archive, "settings/node.json", id) {
		t.Error("the portable archive still carries this installation's identity")
	}
	if !archiveHas(t, archive, "settings/theme.json", "#123456") {
		t.Error("the portable archive stopped carrying the theme; only the identity was meant to stay home")
	}
}

// archiveHas reports whether a gzip-tar archive holds the named member and, when
// `wants` is non-empty, whether its body mentions it.
func archiveHas(t *testing.T, archive []byte, name, wants string) bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if hdr.Name != name {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return wants == "" || contains(string(body), wants)
	}
}

// TestARecordThatCannotBeReadIsLeftToTheOrdinaryPath states the boundary of this
// check: it decides what to hold back, never what to reject. A path that is not a
// keyed record, a record that will not decode, and a key that is free all fall
// through to the restore's normal write, which is where a genuinely bad archive is
// already handled.
func TestARecordThatCannotBeReadIsLeftToTheOrdinaryPath(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	good := `{"key":1,"processId":"beta","version":1}`

	cases := []struct {
		name     string
		dest     string
		incoming string
	}{
		{"a draft is not a keyed record", write("drafts/abc.json", good), good},
		{"a path with no directory at all", filepath.Join(dir, "1"), good},
		{"a key nothing holds here", filepath.Join(dir, "deployments", "99"), good},
		{"the stored record will not decode", write("deployments/2", "{not json"), good},
		{"the archive's record will not decode", write("deployments/3", good), "{not json"},
		{"a stored decision that will not decode", write("decisions/4", "{not json"), `{"key":4,"decisions":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if c, clash := foreignDeployment(dir, tc.dest, []byte(tc.incoming)); clash {
				t.Errorf("held back %+v; this is not the case the check is for", c)
			}
		})
	}

	// A path outside the data directory cannot be made relative to it, and is not this
	// check's to refuse either — restoreDest has already rejected traversal.
	if _, clash := foreignDeployment("relative-dir", string(filepath.Separator)+"elsewhere/deployments/1", []byte(good)); clash {
		t.Error("held back a path outside the data directory")
	}
}

// TestADecisionDeploymentProvidingNothingIsNotMistakenForAnother: a record with no
// decisions provides nothing anyone can bind to, so two of them at one key are the
// same as far as a pin is concerned. Stated rather than left to fall out of a string
// comparison.
func TestADecisionDeploymentProvidingNothingIsNotMistakenForAnother(t *testing.T) {
	empty := persistedDecision{Key: 7}
	if got := deployedDecisionName(empty); got != "no decisions" {
		t.Errorf("deployedDecisionName = %q, want it to say so in words", got)
	}
}

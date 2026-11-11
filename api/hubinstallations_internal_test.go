package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The server-side atlas hub import is the one place that knows both what it fetched
// and what it wrote, so it records where an installed application came from: the
// registry, the key that vouched for the index, the release and its exact digest
// (ADR-0499, issue #1422). These tests hold that record to what the import did.

func hubInstallations(t *testing.T, ts *httptest.Server) []hubInstallation {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + "/api/v1/blueprints/installations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("installations = %d %s", resp.StatusCode, raw)
	}
	var out []hubInstallation
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("installations are not a JSON list: %s", raw)
	}
	return out
}

// TestHubImportRecordsWhereTheApplicationCameFrom: a dry run records nothing; the
// import records the release it installed, pinned to the registry, the signing key,
// the version and the digest; a repeat of the same release keeps the first record.
func TestHubImportRecordsWhereTheApplicationCameFrom(t *testing.T) {
	ts, f := hubImportServer(t)
	digest := f.release.Artifact.Digest

	if got := hubInstallations(t, ts); len(got) != 0 {
		t.Fatalf("a fresh server lists installations: %+v", got)
	}
	if code, out := hubPost(t, ts, "?digest="+digest+"&dryRun=true"); code != http.StatusOK {
		t.Fatalf("dry run = %d %v", code, out)
	}
	if got := hubInstallations(t, ts); len(got) != 0 {
		t.Fatalf("a dry run recorded an installation: %+v", got)
	}

	code, out := hubPost(t, ts, "?digest="+digest)
	if code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	got := hubInstallations(t, ts)
	if len(got) != 1 {
		t.Fatalf("installations = %+v", got)
	}
	rec := got[0]
	want := hubInstallation{
		ApplicationKey: "hub-sample", ApplicationID: out["applicationId"].(string), State: hubInstalled,
		RegistryID: "org.example.hub", IndexURL: f.origin.IndexURL, SignedBy: f.origin.SignedBy,
		PublisherID: "org.example", BlueprintID: "org.example.hub-sample", Version: "1.0.0", Digest: digest,
		CataloguesNotImported: []string{"cat-hub"},
	}
	if rec.RecordedAt == 0 {
		t.Fatal("the record carries no time")
	}
	first := rec
	rec.RecordedAt, rec.InstalledBy = 0, ""
	if !equalInstallations(rec, want) {
		t.Fatalf("record = %+v\nwant     %+v", rec, want)
	}

	if code, out := hubPost(t, ts, "?digest="+digest); code != http.StatusOK {
		t.Fatalf("second import = %d %v", code, out)
	}
	if again := hubInstallations(t, ts); len(again) != 1 || !equalInstallations(again[0], first) {
		t.Fatalf("a repeat of the same release changed the record: %+v, was %+v", again, first)
	}
}

// An import the importer refuses or stops — a change nobody decided — installs
// nothing, so it records nothing, and it leaves an earlier record as it was.
func TestHubImportThatStopsRecordsNothing(t *testing.T) {
	ts, f := hubImportServer(t)
	other := strings.Replace(waitingProcess("proc_hub_sample"), `<userTask id="u"/>`, `<userTask id="u" name="edited here"/>`, 1)
	resp, err := ts.Client().Post(ts.URL+"/api/v1/applications", "application/json", strings.NewReader(`{"name":"Hub sample"}`))
	if err != nil {
		t.Fatal(err)
	}
	var app struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()
	resp, err = ts.Client().Post(ts.URL+"/api/v1/drafts?projectId="+app.ID, "application/xml", strings.NewReader(other))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest); code != http.StatusConflict {
		t.Fatalf("undecided import = %d %v", code, out)
	}
	if got := hubInstallations(t, ts); len(got) != 0 {
		t.Fatalf("an import that stopped recorded an installation: %+v", got)
	}
	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest+"&keep=*"); code != http.StatusOK {
		t.Fatalf("import keeping the local edit = %d %v", code, out)
	}
	installed := hubInstallations(t, ts)
	if len(installed) != 1 || installed[0].State != hubInstalled {
		t.Fatalf("installations after the decided import = %+v", installed)
	}

	// A new package of the release meets the local edit again: nobody decided, the
	// import stops, and the record of the earlier installation stays as it was.
	f.offer(t, strings.Replace(waitingProcess("proc_hub_sample"), `<userTask id="u"/>`, `<userTask id="u" name="changed upstream"/>`, 1))
	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest); code != http.StatusConflict {
		t.Fatalf("undecided import of the new package = %d %v", code, out)
	}
	if got := hubInstallations(t, ts); len(got) != 1 || !equalInstallations(got[0], installed[0]) {
		t.Fatalf("an import that stopped changed the earlier record: %+v, was %+v", got, installed)
	}
}

// The record is design-time state on disk: it is there after a restart, with no
// registry to ask.
func TestHubInstallationSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	_, ts, f, stop := hubImportServerAt(t, dir)
	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	before := hubInstallations(t, ts)
	ts.Close()
	stop()

	srv, _ := openAt(t, dir) // no registry configured at all
	again := httptest.NewServer(srv.Handler())
	t.Cleanup(again.Close)
	after := hubInstallations(t, again)
	if len(after) != 1 || !equalInstallations(after[0], before[0]) {
		t.Fatalf("after a restart = %+v, before %+v", after, before)
	}
}

// The record is written before the application is: when it cannot be written, the
// import writes nothing and says why.
func TestHubImportWithoutAWritableRecordInstallsNothing(t *testing.T) {
	dir := t.TempDir()
	_, ts, f, _ := hubImportServerAt(t, dir)
	store := filepath.Join(dir, "blueprint-installations")
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest)
	if code != http.StatusInternalServerError || !strings.Contains(out["error"].(string), "installation") {
		t.Fatalf("import with an unwritable record = %d %v", code, out)
	}
	resp, err := ts.Client().Get(ts.URL + "/api/v1/applications")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "hub-sample") {
		t.Fatalf("the application was installed without its record: %s", raw)
	}
}

// A record left pending — the server stopped between writing it and finishing the
// import — is listed as pending: what the application holds is not known. The next
// import that completes replaces it with what that import installed.
func TestPendingHubInstallationIsListedAndReplacedByTheNextImport(t *testing.T) {
	dir := t.TempDir()
	srv, ts, f, _ := hubImportServerAt(t, dir)
	left := hubInstallation{ApplicationKey: "hub-sample", State: hubPending, RegistryID: "org.example.hub",
		BlueprintID: "org.example.hub-sample", Version: "0.9.0", Digest: "sha256:" + strings.Repeat("1", 64), RecordedAt: 1}
	if err := srv.hubInstalls.Save(left); err != nil {
		t.Fatal(err)
	}
	if got := hubInstallations(t, ts); len(got) != 1 || got[0].State != hubPending || got[0].Version != "0.9.0" {
		t.Fatalf("pending record = %+v", got)
	}
	if code, out := hubPost(t, ts, "?digest="+f.release.Artifact.Digest); code != http.StatusOK {
		t.Fatalf("import = %d %v", code, out)
	}
	got := hubInstallations(t, ts)
	if len(got) != 1 || got[0].State != hubInstalled || got[0].Version != "1.0.0" || got[0].Digest != f.release.Artifact.Digest {
		t.Fatalf("after the import = %+v", got)
	}
}

func equalInstallations(a, b hubInstallation) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// hubJournal's end, outcome by outcome: what the record holds after the import.
func TestHubJournalEndsAsTheImportDid(t *testing.T) {
	installed := hubInstallation{ApplicationKey: "app", ApplicationID: "id-1", State: hubInstalled,
		RegistryID: "r", BlueprintID: "b", Version: "1.0.0", Digest: "sha256:old", RecordedAt: 1}
	next := hubInstallation{ApplicationKey: "app", State: hubPending,
		RegistryID: "r", BlueprintID: "b", Version: "2.0.0", Digest: "sha256:new", RecordedAt: 2}
	cases := []struct {
		name      string
		before    *hubInstallation
		rec       hubInstallation
		outcome   hubOutcome
		wantState string // "" for no record at all
		wantVer   string
	}{
		{"finished, first install", nil, next, hubFinished, hubInstalled, "2.0.0"},
		{"finished, newer release", &installed, next, hubFinished, hubInstalled, "2.0.0"},
		{"finished, same package again keeps the first record", &installed, func() hubInstallation {
			r := installed
			r.State, r.ApplicationID, r.RecordedAt = hubPending, "", 9
			return r
		}(), hubFinished, hubInstalled, "1.0.0"},
		{"stopped, nothing before", nil, next, hubStopped, "", ""},
		{"stopped, earlier record restored", &installed, next, hubStopped, hubInstalled, "1.0.0"},
		{"failed halfway stays pending", &installed, next, hubUnknown, hubPending, "2.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, err := newHubInstallationStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if c.before != nil {
				if err := store.Save(*c.before); err != nil {
					t.Fatal(err)
				}
			}
			j := &hubJournal{store: store, rec: c.rec}
			if err := j.begin(); err != nil {
				t.Fatal(err)
			}
			if mid, _, _ := store.Get("app"); mid.State != hubPending {
				t.Fatalf("after begin the record is %q, not pending", mid.State)
			}
			if err := j.end(sourceImportResult{ApplicationID: "id-1"}, c.outcome); err != nil {
				t.Fatal(err)
			}
			got, found, err := store.Get("app")
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case c.wantState == "" && found:
				t.Fatalf("a record is left: %+v", got)
			case c.wantState != "" && (!found || got.State != c.wantState || got.Version != c.wantVer):
				t.Fatalf("record = %+v (found %v), want %s %s", got, found, c.wantState, c.wantVer)
			}
			if c.name == "finished, same package again keeps the first record" && got.RecordedAt != 1 {
				t.Fatalf("the first record was replaced: %+v", got)
			}
		})
	}
}

// hubOutcomeOf tells the importer's errors apart: a refusal and an undecided change
// come before any write, anything else may not.
func TestHubOutcomeOfTheImportersErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want hubOutcome
	}{
		{nil, hubFinished},
		{sourceRefusal{http.StatusForbidden, "x"}, hubStopped},
		{fmt.Errorf("wrapped: %w", sourceUndecided{}), hubStopped},
		{os.ErrPermission, hubUnknown},
	} {
		if got := hubOutcomeOf(c.err); got != c.want {
			t.Errorf("hubOutcomeOf(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

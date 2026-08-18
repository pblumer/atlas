package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
)

// ADR-0134 Phase 4, slice 4b: the git binding. These drive the real transport
// against a local bare repository — bind, commit, clone into a second server,
// import — and pin the refusal that the whole design rests on: Atlas never
// merges, so a branch that moved since the last sync stops a commit.
//
// The fixtures use file:// remotes, which is why binding to one is admin-gated in
// the handler; with auth off (as here) every caller is an operator.

// bareRepo creates an empty bare repository and returns its file:// URL.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(dir, true); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	return "file://" + dir
}

// bindGit binds an application to a repository.
func bindGit(t *testing.T, ts *httptest.Server, appID, repoURL string) {
	t.Helper()
	body := `{"repoUrl":"` + repoURL + `","branch":"main"}`
	code, out := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/git", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", code, out)
	}
}

type gitCommitResp struct {
	Commit  string `json:"commit"`
	Changed bool   `json:"changed"`
	Message string `json:"message"`
}

type gitStatusResp struct {
	Bound        bool   `json:"bound"`
	RepoURL      string `json:"repoUrl"`
	Branch       string `json:"branch"`
	LastCommit   string `json:"lastCommit"`
	LastAction   string `json:"lastAction"`
	RemoteCommit string `json:"remoteCommit"`
	RemoteError  string `json:"remoteError"`
	Diverged     bool   `json:"diverged"`
}

// commitApp posts a commit and returns the decoded result.
func commitApp(t *testing.T, ts *httptest.Server, appID, message string) (int, gitCommitResp, []byte) {
	t.Helper()
	body := ""
	if message != "" {
		body = `{"message":` + strconv.Quote(message) + `}`
	}
	code, out := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/commit", body, "application/json")
	var res gitCommitResp
	if code == http.StatusOK {
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("decode commit: %v (%s)", err, out)
		}
	}
	return code, res, out
}

// gitStatus reads the binding view.
func gitStatus(t *testing.T, ts *httptest.Server, appID string) gitStatusResp {
	t.Helper()
	code, out := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/git", "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, out)
	}
	var res gitStatusResp
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode status: %v (%s)", err, out)
	}
	return res
}

// repoFiles clones a repository's branch and returns its files by path.
func repoFiles(t *testing.T, repoURL string) map[string]string {
	t.Helper()
	fs := memfs.New()
	repo, err := git.Clone(memory.NewStorage(), fs, &git.CloneOptions{
		URL: repoURL, ReferenceName: plumbing.NewBranchReferenceName("main"), SingleBranch: true,
	})
	if err != nil {
		t.Fatalf("clone %s: %v", repoURL, err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	out := map[string]string{}
	if err := tree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return err
		}
		out[f.Name] = content
		return nil
	}); err != nil {
		t.Fatalf("walk tree: %v", err)
	}
	return out
}

// pushForeignCommit writes a file into the repository behind Atlas's back, the way
// a colleague pushing from their own machine would.
func pushForeignCommit(t *testing.T, repoURL, name, content string) string {
	t.Helper()
	fs := memfs.New()
	branch := plumbing.NewBranchReferenceName("main")
	repo, err := git.Clone(memory.NewStorage(), fs, &git.CloneOptions{
		URL: repoURL, ReferenceName: branch, SingleBranch: true,
	})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		// The repository has no commits yet: seed it instead of cloning it.
		fs = memfs.New()
		if repo, err = git.Init(memory.NewStorage(), fs); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{repoURL}}); err != nil {
			t.Fatalf("create remote: %v", err)
		}
		if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch)); err != nil {
			t.Fatalf("set HEAD: %v", err)
		}
	} else if err != nil {
		t.Fatalf("clone: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	f, err := fs.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatalf("add: %v", err)
	}
	hash, err := wt.Commit("a colleague's change", &git.CommitOptions{
		Author: &object.Signature{Name: "Kollegin", Email: "k@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec(branch + ":" + branch)},
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	return hash.String()
}

// TestGitCommitThenImportIntoAnotherServer is the loop the binding exists for: an
// application's source is committed to a repository, and a second Atlas server
// that has never seen the application reads it back out of the same repository.
func TestGitCommitThenImportIntoAnotherServer(t *testing.T) {
	repo := bareRepo(t)

	origin := newTestServer(t)
	appID := mkApp(t, origin, "Mitarbeiter Onboarding")
	saveDraftIn(t, origin, appID, "onboarding", "Onboarding")
	saveFormIn(t, origin, appID, "neuer-mitarbeiter", "Neuer Mitarbeiter")
	bindGit(t, origin, appID, repo)

	code, res, body := commitApp(t, origin, appID, "Erste Fassung")
	if code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", code, body)
	}
	if !res.Changed || res.Commit == "" {
		t.Fatalf("commit result = %+v", res)
	}

	// The repository holds the curated layout, in native files.
	files := repoFiles(t, repo)
	for _, want := range []string{"atlas.json", "processes/onboarding.bpmn", "forms/neuer-mitarbeiter.form.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("repository is missing %q; it holds %v", want, keysOf(files))
		}
	}
	if !strings.Contains(files["processes/onboarding.bpmn"], `<startEvent id="s"/>`) {
		t.Errorf("the .bpmn is not the diagram: %s", files["processes/onboarding.bpmn"])
	}
	if !strings.Contains(files["atlas.json"], `"key": "mitarbeiter-onboarding"`) {
		t.Errorf("manifest lacks the portable key: %s", files["atlas.json"])
	}

	// The binding now knows where the branch is.
	st := gitStatus(t, origin, appID)
	if st.LastCommit != res.Commit || st.LastAction != "commit" || st.Diverged {
		t.Errorf("status = %+v, want lastCommit=%s and no divergence", st, res.Commit)
	}

	// A second server, which has never seen this application, reads it from git.
	clone := newTestServer(t)
	cloneApp := mkApp(t, clone, "Platzhalter")
	bindGit(t, clone, cloneApp, repo)
	code, body = doReq(t, clone, http.MethodPost, "/api/v1/applications/"+cloneApp+"/git/import", "", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("import into a mismatched application = %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), "different application") {
		t.Errorf("refusal does not explain the key mismatch: %s", body)
	}
}

// TestGitImportIntoTheMatchingApplication: an import lands when the repository's
// key names the application the binding is on — the identity check, not a name
// match.
func TestGitImportIntoTheMatchingApplication(t *testing.T) {
	repo := bareRepo(t)
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Onboarding")
	saveDraftIn(t, origin, appID, "onboarding", "Onboarding")
	bindGit(t, origin, appID, repo)
	if code, _, body := commitApp(t, origin, appID, "seed"); code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", code, body)
	}

	// A second server whose application carries the same key: created by importing
	// the archive first, which is how a clone gets its identity.
	clone := newTestServer(t)
	archive := exportSource(t, origin, appID)
	code, imported, body := importSource(t, clone, archive)
	if code != http.StatusOK {
		t.Fatalf("seed import status=%d body=%s", code, body)
	}
	bindGit(t, clone, imported.ApplicationID, repo)

	// Now change the source upstream and pull it down.
	saveDraftIn(t, origin, appID, "zweiter", "Zweiter Prozess")
	if code, _, body := commitApp(t, origin, appID, "zweiter Prozess"); code != http.StatusOK {
		t.Fatalf("second commit status=%d body=%s", code, body)
	}
	code, body = doReq(t, clone, http.MethodPost, "/api/v1/applications/"+imported.ApplicationID+"/git/import", "", "application/json")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	var res struct {
		sourceImportResult
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if res.Processes != 2 || res.Commit == "" {
		t.Errorf("import result = %+v, want both processes and a commit", res)
	}
	code, xml := doReq(t, clone, http.MethodGet, "/api/v1/drafts/zweiter/xml", "", "")
	if code != http.StatusOK {
		t.Errorf("the imported draft is missing: %d %s", code, xml)
	}
	// And the binding recorded where it read from.
	if st := gitStatus(t, clone, imported.ApplicationID); st.LastAction != "import" || st.LastCommit != res.Commit {
		t.Errorf("status = %+v, want lastAction=import at %s", st, res.Commit)
	}
}

// TestGitCommitRefusedWhenBranchMoved is the ADR-0134 decision in force: Atlas
// never merges, so a branch someone else pushed to stops the commit — with both
// commits named, because "it diverged" is not something an operator can act on.
func TestGitCommitRefusedWhenBranchMoved(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, repo)
	code, first, body := commitApp(t, ts, appID, "seed")
	if code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	// A colleague pushes; Atlas has not seen it.
	foreign := pushForeignCommit(t, repo, "README.md", "# Onboarding\n")

	// The status view says so before anyone tries.
	st := gitStatus(t, ts, appID)
	if !st.Diverged || st.RemoteCommit != foreign {
		t.Errorf("status = %+v, want diverged at %s", st, foreign)
	}

	saveDraftIn(t, ts, appID, "p2", "Zweiter")
	code, _, body = commitApp(t, ts, appID, "zweiter")
	if code != http.StatusConflict {
		t.Fatalf("commit onto a moved branch = %d %s, want 409", code, body)
	}
	for _, want := range []string{first.Commit[:8], foreign[:8], "import first"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("refusal %s does not mention %q", body, want)
		}
	}

	// Importing resolves it, and the commit then goes through.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/import", "", "application/json"); code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	code, second, body := commitApp(t, ts, appID, "zweiter")
	if code != http.StatusOK {
		t.Fatalf("commit after import = %d %s", code, body)
	}
	if second.Commit == first.Commit {
		t.Error("the commit did not advance the branch")
	}
	// The colleague's file survived: Atlas rewrites only the paths it owns.
	files := repoFiles(t, repo)
	if _, ok := files["README.md"]; !ok {
		t.Errorf("Atlas deleted a file it does not own; repo holds %v", keysOf(files))
	}
	if _, ok := files["processes/zweiter.bpmn"]; !ok {
		t.Errorf("the new process is missing; repo holds %v", keysOf(files))
	}
}

// TestGitCommitRefusedIntoUnknownHistory: a repository that already has commits
// Atlas has never seen is not a blank page to write over.
func TestGitCommitRefusedIntoUnknownHistory(t *testing.T) {
	repo := bareRepo(t)
	seed := newTestServer(t)
	seedApp := mkApp(t, seed, "Fremd")
	saveDraftIn(t, seed, seedApp, "fremd", "Fremd")
	bindGit(t, seed, seedApp, repo)
	if code, _, body := commitApp(t, seed, seedApp, "seed"); code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	ts := newTestServer(t)
	appID := mkApp(t, ts, "Meins")
	bindGit(t, ts, appID, repo)
	code, _, body := commitApp(t, ts, appID, "meins")
	if code != http.StatusConflict {
		t.Fatalf("commit into unknown history = %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), "never seen") {
		t.Errorf("refusal = %s", body)
	}
}

// TestGitCommitIsANoOpWhenNothingChanged: a repository full of empty commits is
// noise a reviewer has to read past.
func TestGitCommitIsANoOpWhenNothingChanged(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, repo)
	code, first, body := commitApp(t, ts, appID, "seed")
	if code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", code, body)
	}
	code, again, body := commitApp(t, ts, appID, "nochmal")
	if code != http.StatusOK {
		t.Fatalf("second commit status=%d body=%s", code, body)
	}
	if again.Changed || again.Commit != first.Commit {
		t.Errorf("second commit = %+v, want no change at %s", again, first.Commit)
	}
}

// TestGitBindingValidation: the transport rules are refusals with reasons, not
// silent downgrades.
func TestGitBindingValidation(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	cases := []struct {
		name, body, want string
	}{
		{"empty", `{"repoUrl":""}`, "required"},
		{"plaintext http", `{"repoUrl":"http://example.com/x.git"}`, "must use https"},
		{"ssh url", `{"repoUrl":"ssh://git@example.com/x.git"}`, "ssh remotes are not supported"},
		{"scp syntax", `{"repoUrl":"git@example.com:org/x.git"}`, "ssh remotes are not supported"},
		{"unknown scheme", `{"repoUrl":"ftp://example.com/x.git"}`, "must use https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/git", c.body, "application/json")
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", code, body)
			}
			if !strings.Contains(string(body), c.want) {
				t.Errorf("body=%s, want %q", body, c.want)
			}
		})
	}
	// https is accepted (nothing is contacted at bind time).
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/git",
		`{"repoUrl":"https://example.com/org/app.git/","credentialRef":"git-token"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("https bind = %d %s", code, body)
	}
	var view gitStatusResp
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.RepoURL != "https://example.com/org/app.git" || view.Branch != "main" {
		t.Errorf("binding = %+v, want a normalized URL and the default branch", view)
	}
	// The credential is a handle, never a value.
	if strings.Contains(string(body), "password") || strings.Contains(string(body), "secret") {
		t.Errorf("binding response looks like it carries a credential: %s", body)
	}
}

// TestGitOperationsNeedABinding: import and commit on an unbound application say
// so rather than failing obscurely.
func TestGitOperationsNeedABinding(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	if st := gitStatus(t, ts, appID); st.Bound {
		t.Errorf("a fresh application reports a binding: %+v", st)
	}
	for _, path := range []string{"/git/import", "/git/commit"} {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+path, "", "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", path, code, body)
		}
		if !strings.Contains(string(body), "not bound") {
			t.Errorf("%s body = %s", path, body)
		}
	}
}

// TestGitImportRefusesEmptyRepository: nothing to import is not an error state to
// guess at — it is a repository waiting for its first commit.
func TestGitImportRefusesEmptyRepository(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	bindGit(t, ts, appID, bareRepo(t))
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/import", "", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "no commits yet") {
		t.Errorf("body=%s", body)
	}
}

// TestGitUnbindForgetsTheRepository: unbinding is Atlas forgetting, not a deletion
// — and it is idempotent.
func TestGitUnbindForgetsTheRepository(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, repo)
	if code, _, body := commitApp(t, ts, appID, "seed"); code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", code, body)
	}
	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+appID+"/git", "", ""); code != http.StatusNoContent {
			t.Fatalf("unbind %d = %d %s, want 204", i, code, body)
		}
	}
	if st := gitStatus(t, ts, appID); st.Bound {
		t.Errorf("still bound after unbind: %+v", st)
	}
	// The repository still holds what was committed.
	if files := repoFiles(t, repo); len(files) == 0 {
		t.Error("unbinding emptied the repository")
	}
}

// TestGitRebindToAnotherRepositoryForgetsTheCommit: the recorded commit describes
// one branch's history, so carrying it to another repository would compare the
// next commit against a hash from somewhere else entirely.
func TestGitRebindToAnotherRepositoryForgetsTheCommit(t *testing.T) {
	first, second := bareRepo(t), bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, first)
	if code, _, body := commitApp(t, ts, appID, "seed"); code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", code, body)
	}
	if st := gitStatus(t, ts, appID); st.LastCommit == "" {
		t.Fatal("no commit recorded after the first commit")
	}
	bindGit(t, ts, appID, second)
	if st := gitStatus(t, ts, appID); st.LastCommit != "" {
		t.Errorf("rebinding kept the old commit %q", st.LastCommit)
	}
	// Rebinding to the same repository keeps it, so a no-op edit does not force a
	// re-import.
	bindGit(t, ts, appID, second)
	if code, _, body := commitApp(t, ts, appID, "into the second"); code != http.StatusOK {
		t.Fatalf("commit into the second repository = %d %s", code, body)
	}
	before := gitStatus(t, ts, appID).LastCommit
	bindGit(t, ts, appID, second)
	if after := gitStatus(t, ts, appID).LastCommit; after != before {
		t.Errorf("rebinding to the same repository lost the commit: %q → %q", before, after)
	}
}

// TestGitStatusReportsAnUnreachableRemote: the view is best-effort, so a remote
// that cannot be reached is reported as such rather than failing the read.
func TestGitStatusReportsAnUnreachableRemote(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	bindGit(t, ts, appID, "file://"+filepath.Join(t.TempDir(), "does-not-exist.git"))
	st := gitStatus(t, ts, appID)
	if !st.Bound {
		t.Fatalf("status = %+v, want the binding reported", st)
	}
	if st.RemoteError == "" {
		t.Errorf("status = %+v, want an unreachable remote reported", st)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestGitBindingUnderAuth: a repository binding is as sensitive as the
// application it belongs to, and a file:// remote — the one scheme that reaches
// the server's own disk — takes the operator role rather than an editor's.
func TestGitBindingUnderAuth(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for _, u := range []string{"alice", "bob"} {
		if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+u+`","password":"password1"}`); code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", u, code, body)
		}
	}
	alice, bob := newClient(t), newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK ||
		login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("login failed")
	}

	code, body := cReq(t, alice, ts, "POST", "/api/v1/applications", `{"name":"Geheim"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Alice may bind an https remote; Bob cannot even see the application.
	if code, body := cReq(t, alice, ts, "PUT", "/api/v1/applications/"+app.ID+"/git",
		`{"repoUrl":"https://example.com/org/app.git"}`); code != http.StatusOK {
		t.Fatalf("alice bind = %d %s", code, body)
	}
	for _, op := range []struct{ method, path, body string }{
		{"GET", "/git", ""},
		{"PUT", "/git", `{"repoUrl":"https://evil.example/x.git"}`},
		{"DELETE", "/git", ""},
		{"POST", "/git/import", ""},
		{"POST", "/git/commit", ""},
	} {
		code, body := cReq(t, bob, ts, op.method, "/api/v1/applications/"+app.ID+op.path, op.body)
		if code != http.StatusNotFound {
			t.Errorf("bob %s %s = %d %s, want 404", op.method, op.path, code, body)
		}
	}

	// A file:// remote is an operator's call, even on one's own application.
	local := "file://" + filepath.Join(t.TempDir(), "local.git")
	code, body = cReq(t, alice, ts, "PUT", "/api/v1/applications/"+app.ID+"/git",
		`{"repoUrl":"`+local+`"}`)
	if code != http.StatusForbidden {
		t.Fatalf("alice binding a local path = %d %s, want 403", code, body)
	}
	if !strings.Contains(string(body), "admin") {
		t.Errorf("refusal = %s", body)
	}
	// The admin may, and the earlier https binding is unchanged by the refusal.
	var view gitStatusResp
	code, body = cReq(t, admin, ts, "GET", "/api/v1/applications/"+app.ID+"/git", "")
	if code != http.StatusOK {
		t.Fatalf("admin status = %d %s", code, body)
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.RepoURL != "https://example.com/org/app.git" {
		t.Errorf("the refused bind changed the binding: %+v", view)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/applications/"+app.ID+"/git",
		`{"repoUrl":"`+local+`"}`); code != http.StatusOK {
		t.Fatalf("admin binding a local path = %d %s", code, body)
	}

	// And the loop works end to end under auth: alice commits her application and
	// reads it straight back, with the history naming her rather than the server.
	if _, err := git.PlainInit(strings.TrimPrefix(local, "file://"), true); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if code, body := cReqCT(t, alice, ts, "POST", "/api/v1/drafts?projectId="+app.ID,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p1" name="Erster" isExecutable="true"><startEvent id="s"/></process>
</definitions>`, "application/xml"); code != http.StatusOK {
		t.Fatalf("alice save draft = %d %s", code, body)
	}
	if code, body := cReq(t, alice, ts, "POST", "/api/v1/applications/"+app.ID+"/git/commit",
		`{"message":"von alice"}`); code != http.StatusOK {
		t.Fatalf("alice commit = %d %s", code, body)
	}
	code, body = cReq(t, alice, ts, "POST", "/api/v1/applications/"+app.ID+"/git/import", "")
	if code != http.StatusOK {
		t.Fatalf("alice import = %d %s", code, body)
	}
	if !strings.Contains(string(body), `"processes":1`) {
		t.Errorf("import result = %s", body)
	}
	// The commit is attributed to the person who made it.
	fs := memfs.New()
	repo, err := git.Clone(memory.NewStorage(), fs, &git.CloneOptions{
		URL: local, ReferenceName: plumbing.NewBranchReferenceName("main"), SingleBranch: true,
	})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	if commit.Author.Name != "alice" {
		t.Errorf("commit author = %q, want the signed-in user", commit.Author.Name)
	}
}

// TestGitBindingRefusedOnProtectedApplication: a platform-managed system project
// (ADR-0122) is bootstrap-deployed, and nothing in the design-time API writes to
// it — a git binding least of all.
func TestGitBindingRefusedOnProtectedApplication(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/applications/system/git",
		`{"repoUrl":"https://example.com/x.git"}`, "application/json")
	if code != http.StatusForbidden {
		t.Fatalf("bind on the system project = %d %s, want 403", code, body)
	}
	for _, path := range []string{"/git/import", "/git/commit"} {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/system"+path, "", "application/json"); code != http.StatusForbidden {
			t.Errorf("%s on the system project = %d %s, want 403", path, code, body)
		}
	}
}

// TestGitImportAdoptsAKeylessApplication: an application from before ADR-0134 has
// no portable key. Binding it to a repository and importing is a statement about
// which application that repository holds, so it adopts the manifest's key.
func TestGitImportAdoptsAKeylessApplication(t *testing.T) {
	repo := bareRepo(t)
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Onboarding")
	saveDraftIn(t, origin, appID, "p1", "Erster")
	bindGit(t, origin, appID, repo)
	if code, _, body := commitApp(t, origin, appID, "seed"); code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	// A second server whose application carries a *different* key refuses.
	other := newTestServer(t)
	otherApp := mkApp(t, other, "Ganz anders")
	bindGit(t, other, otherApp, repo)
	code, body := doReq(t, other, http.MethodPost, "/api/v1/applications/"+otherApp+"/git/import", "", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("import into a differently-keyed application = %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), "onboarding") || !strings.Contains(string(body), "ganz-anders") {
		t.Errorf("refusal names neither key: %s", body)
	}
}

// TestGitImportReportsAnUnreadableRepository: a repository that is not an
// application source tree is said to be one, with the reason, rather than
// half-imported.
func TestGitImportReportsAnUnreadableRepository(t *testing.T) {
	repo := bareRepo(t)
	// A repository with content but no manifest.
	pushForeignCommit(t, repo, "README.md", "# not an application\n")

	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	bindGit(t, ts, appID, repo)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/import", "", "application/json")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s, want 422", code, body)
	}
	if !strings.Contains(string(body), "atlas.json") {
		t.Errorf("body=%s, want the missing manifest named", body)
	}
}

// TestGitOperationsOnAnUnreachableRemote: git is a network dependency, so its
// failures are reported as the remote's, not as this server's.
func TestGitOperationsOnAnUnreachableRemote(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, "file://"+filepath.Join(t.TempDir(), "missing.git"))
	for _, path := range []string{"/git/import", "/git/commit"} {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+path, "", "application/json")
		if code != http.StatusBadGateway {
			t.Errorf("%s = %d %s, want 502", path, code, body)
		}
	}
}

// TestGitCommitPropagatesADeletion: a repository is the application's source, so
// an artifact deleted in Atlas has to disappear from the tree too. Only the paths
// Atlas owns are rewritten, so nothing else in the repository is touched.
func TestGitCommitPropagatesADeletion(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "bleibt", "Bleibt")
	saveDraftIn(t, ts, appID, "geht", "Geht")
	saveFormIn(t, ts, appID, "f1", "Formular")
	bindGit(t, ts, appID, repo)
	if code, _, body := commitApp(t, ts, appID, "beide"); code != http.StatusOK {
		t.Fatalf("first commit status=%d body=%s", code, body)
	}
	if _, ok := repoFiles(t, repo)["processes/geht.bpmn"]; !ok {
		t.Fatal("the draft never reached the repository")
	}

	// A colleague's file, which Atlas must leave alone across the deletion.
	pushForeignCommit(t, repo, "README.md", "# Onboarding\n")
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/import", "", "application/json"); code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}

	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/drafts/geht", "", ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete draft status=%d body=%s", code, body)
	}
	if code, res, body := commitApp(t, ts, appID, "einen entfernt"); code != http.StatusOK || !res.Changed {
		t.Fatalf("second commit status=%d changed=%v body=%s", code, res.Changed, body)
	}

	files := repoFiles(t, repo)
	if _, ok := files["processes/geht.bpmn"]; ok {
		t.Errorf("the deleted draft is still in the repository: %v", keysOf(files))
	}
	for _, want := range []string{"processes/bleibt.bpmn", "forms/formular.form.json", "README.md"} {
		if _, ok := files[want]; !ok {
			t.Errorf("%q went missing; repo holds %v", want, keysOf(files))
		}
	}
	// The manifest no longer indexes the removed process.
	if strings.Contains(files["atlas.json"], `"geht"`) {
		t.Errorf("the manifest still lists the deleted process: %s", files["atlas.json"])
	}
}

// TestGitCommitRefusedWhenTheRepositoryWasReset: Atlas holds a commit the remote
// no longer has. That is not an empty repository to seed — it is a history that
// was thrown away, and re-seeding it silently would hide that.
func TestGitCommitRefusedWhenTheRepositoryWasReset(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, repo)
	code, first, body := commitApp(t, ts, appID, "seed")
	if code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	// The repository is replaced by an empty one at the same path.
	path := strings.TrimPrefix(repo, "file://")
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("remove repo: %v", err)
	}
	if _, err := git.PlainInit(path, true); err != nil {
		t.Fatalf("re-init: %v", err)
	}

	saveDraftIn(t, ts, appID, "p2", "Zweiter")
	code, _, body = commitApp(t, ts, appID, "zweiter")
	if code != http.StatusConflict {
		t.Fatalf("commit into a reset repository = %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), first.Commit[:8]) {
		t.Errorf("refusal does not name the commit Atlas holds: %s", body)
	}
}

// TestGitStatusReportsAMissingBranch: binding to a branch the remote does not have
// is a mistake worth naming, not a silent "in sync".
func TestGitStatusReportsAMissingBranch(t *testing.T) {
	repo := bareRepo(t)
	seed := newTestServer(t)
	seedApp := mkApp(t, seed, "Onboarding")
	saveDraftIn(t, seed, seedApp, "p1", "Erster")
	bindGit(t, seed, seedApp, repo)
	if code, _, body := commitApp(t, seed, seedApp, "seed"); code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	code, body := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/git",
		`{"repoUrl":"`+repo+`","branch":"release"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", code, body)
	}
	st := gitStatus(t, ts, appID)
	if !strings.Contains(st.RemoteError, "release") {
		t.Errorf("status = %+v, want the missing branch named", st)
	}
}

// TestGitCommitDefaultsItsMessage: a commit always carries a message, because a
// history of blank commits tells a reviewer nothing.
func TestGitCommitDefaultsItsMessage(t *testing.T) {
	repo := bareRepo(t)
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Mitarbeiter Onboarding")
	saveDraftIn(t, ts, appID, "p1", "Erster")
	bindGit(t, ts, appID, repo)
	// No body at all, and a body with a blank message: both take the default.
	for _, body := range []string{"", `{"message":"   "}`} {
		code, out := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/git/commit", body, "application/json")
		if code != http.StatusOK {
			t.Fatalf("commit(%q) status=%d body=%s", body, code, out)
		}
		var res gitCommitResp
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if res.Message != "Update Mitarbeiter Onboarding" {
			t.Errorf("message = %q, want the application-named default", res.Message)
		}
	}
}

// TestGitEndpointsRejectMalformedBodies: a broken body is a 400 with a reason, not
// a partially applied change.
func TestGitEndpointsRejectMalformedBodies(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Onboarding")
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/git"},
		{http.MethodPost, "/git/commit"},
	} {
		code, body := doReq(t, ts, c.method, "/api/v1/applications/"+appID+c.path, "{not json", "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("%s %s = %d %s, want 400", c.method, c.path, code, body)
		}
		if !strings.Contains(string(body), "invalid JSON") {
			t.Errorf("%s %s body = %s", c.method, c.path, body)
		}
	}
}

// TestGitImportRefusesAForeignArtifact: the repository's tree names a process that
// belongs to a different application on this server. Adopting it would move it out
// from under whoever is working in it, so the import is refused whole.
func TestGitImportRefusesAForeignArtifact(t *testing.T) {
	repo := bareRepo(t)
	origin := newTestServer(t)
	originApp := mkApp(t, origin, "Onboarding")
	saveDraftIn(t, origin, originApp, "geteilt", "Geteilt")
	bindGit(t, origin, originApp, repo)
	if code, _, body := commitApp(t, origin, originApp, "seed"); code != http.StatusOK {
		t.Fatalf("seed commit status=%d body=%s", code, body)
	}

	// A second server where that process id already belongs to another application.
	ts := newTestServer(t)
	mine := mkApp(t, ts, "Onboarding") // same name, so the derived key matches
	other := mkApp(t, ts, "Andere")
	saveDraftIn(t, ts, other, "geteilt", "Meins")
	bindGit(t, ts, mine, repo)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+mine+"/git/import", "", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("import = %d %s, want 409", code, body)
	}
	if !strings.Contains(string(body), "geteilt") {
		t.Errorf("refusal does not name the artifact: %s", body)
	}
}

package api

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
)

// ADR-0134 slice 4b, the parts with no network in them: the transport rules, the
// binding store, and what Atlas considers its own in somebody else's repository.

func TestValidateRepoURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://example.com/org/app.git", "https://example.com/org/app.git"},
		{"  https://example.com/org/app.git/  ", "https://example.com/org/app.git"},
		// Plaintext is allowed exactly where it does not cross a network.
		{"http://localhost:3000/app.git", "http://localhost:3000/app.git"},
		{"http://127.0.0.1/app.git", "http://127.0.0.1/app.git"},
	}
	for _, c := range ok {
		got, isLocal, err := validateRepoURL(c.in)
		if err != nil {
			t.Errorf("validateRepoURL(%q) = error %v", c.in, err)
			continue
		}
		if got != c.want || isLocal {
			t.Errorf("validateRepoURL(%q) = %q, local=%v; want %q, local=false", c.in, got, isLocal, c.want)
		}
	}

	// file:// is accepted and flagged, because it reaches this server's own disk
	// and the handler gates it on the operator role.
	got, isLocal, err := validateRepoURL("file:///srv/repos/app.git")
	if err != nil || !isLocal || got != "file:///srv/repos/app.git" {
		t.Errorf("file URL = %q, local=%v, err=%v", got, isLocal, err)
	}

	bad := map[string]string{
		"":                             "required",
		"   ":                          "required",
		"http://example.com/app.git":   "must use https",
		"ssh://git@example.com/a.git":  "ssh remotes are not supported",
		"git://example.com/a.git":      "ssh remotes are not supported",
		"git@example.com:org/app.git":  "ssh remotes are not supported",
		"ftp://example.com/app.git":    "must use https",
		"https:///no-host":             "must include a host",
		"http://":                      "must include a host",
		"file://":                      "must include a path",
		"https://exa mple.com/app.git": "not a valid URL",
	}
	for in, want := range bad {
		_, _, err := validateRepoURL(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("validateRepoURL(%q) = %v, want an error containing %q", in, err, want)
		}
	}
}

func TestGitBindingStore(t *testing.T) {
	dir := t.TempDir()
	store, err := newGitBindingStore(filepath.Join(dir, "git-bindings"))
	if err != nil {
		t.Fatalf("newGitBindingStore: %v", err)
	}
	for _, rec := range []gitBinding{
		{ApplicationID: "a2", RepoURL: "https://example.com/b.git", Branch: "main", BoundAt: 20},
		{ApplicationID: "a1", RepoURL: "https://example.com/a.git", Branch: "main", BoundAt: 10},
	} {
		if err := store.save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	got, ok, err := store.get("a1")
	if err != nil || !ok || got.RepoURL != "https://example.com/a.git" {
		t.Fatalf("get = %+v, ok=%v, err=%v", got, ok, err)
	}
	if _, ok, err := store.get("nope"); ok || err != nil {
		t.Errorf("missing binding = ok %v, err %v", ok, err)
	}

	// Foreign files in the directory are ignored rather than breaking the listing.
	if err := os.MkdirAll(filepath.Join(store.dir, "a-subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.dir, "README.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write non-hex json: %v", err)
	}
	if _, ok, err := store.get("a2"); !ok || err != nil {
		t.Fatalf("get a2 = ok %v, err %v", ok, err)
	}

	// Delete is idempotent, and a corrupt record is reported rather than skipped.
	for i := 0; i < 2; i++ {
		if err := store.delete("a1"); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := os.WriteFile(store.fileFor("a3"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, _, err := store.get("a3"); err == nil {
		t.Error("a corrupt record was read as valid")
	}
}

func TestNewGitBindingStoreReportsFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := newGitBindingStore(filepath.Join(blocker, "git-bindings")); err == nil {
		t.Fatal("a store that cannot create its directory was reported as fine")
	}
	// A store whose directory does not exist cannot delete either: the record is
	// absent (which is fine) but the directory fsync that follows is not.
	broken := &gitBindingStore{dir: filepath.Join(blocker, "git-bindings")}
	if err := broken.delete("a1"); err == nil {
		t.Error("delete on a missing directory was reported as fine")
	}
}

// TestOwnedTreePaths: Atlas reads and rewrites only the curated layout, so a
// repository may hold anything else without Atlas importing it as an artifact or
// deleting it on commit.
func TestOwnedTreePaths(t *testing.T) {
	owned := []string{"atlas.json", "processes/x.bpmn", "forms/y.form.json", "./atlas.json"}
	for _, p := range owned {
		if !ownedTreePath(p) {
			t.Errorf("ownedTreePath(%q) = false, want true", p)
		}
	}
	foreign := []string{
		"README.md", ".github/workflows/ci.yml", "processes/notes.txt",
		"forms/schema.json", "decisions/x.dmn", "processes/nested/x.bpmn", "x.bpmn",
	}
	for _, p := range foreign {
		if ownedTreePath(p) {
			t.Errorf("ownedTreePath(%q) = true, want false", p)
		}
	}
}

// TestReadSourceTreeFromIgnoresForeignFiles builds a worktree the way a real
// repository looks — Atlas's tree plus a README and a CI directory — and checks
// only the layout comes back, in a deterministic order.
func TestReadSourceTreeFromIgnoresForeignFiles(t *testing.T) {
	fsys := memfs.New()
	write := func(name, content string) {
		t.Helper()
		if dir := filepath.Dir(name); dir != "." {
			if err := fsys.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		f, err := fsys.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", name, err)
		}
	}
	write("atlas.json", "{}\n")
	write("processes/zeta.bpmn", "<z/>\n")
	write("processes/alpha.bpmn", "<a/>\n")
	write("forms/f.form.json", "{}\n")
	write("README.md", "# hello\n")
	write(".github/workflows/ci.yml", "on: push\n")
	write("processes/scratch.txt", "notes\n")

	files, err := readSourceTreeFrom(fsys)
	if err != nil {
		t.Fatalf("readSourceTreeFrom: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := "atlas.json,forms/f.form.json,processes/alpha.bpmn,processes/zeta.bpmn"
	if got := strings.Join(paths, ","); got != want {
		t.Errorf("paths = %q, want %q", got, want)
	}
}

// TestReadWholeFileRespectsTheBudget: a repository must not be able to ask this
// server to hold an unbounded amount in memory.
func TestReadWholeFileRespectsTheBudget(t *testing.T) {
	fsys := memfs.New()
	f, err := fsys.Create("atlas.json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Write([]byte("{}")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := readWholeFile(fsys, "atlas.json", 0); err == nil {
		t.Error("an exhausted budget was accepted")
	}
	if _, err := readWholeFile(fsys, "missing.json", 100); err == nil {
		t.Error("a missing file was read successfully")
	}
	data, err := readWholeFile(fsys, "atlas.json", 100)
	if err != nil || string(data) != "{}" {
		t.Errorf("read = %q, %v", data, err)
	}
}

// TestGitRemoteAuth: a token is presented only where it can be, and a local
// remote never carries one.
func TestGitRemoteAuth(t *testing.T) {
	if a := (gitRemote{URL: "https://example.com/a.git"}).auth(); a != nil {
		t.Errorf("no credential should mean no auth, got %v", a)
	}
	if a := (gitRemote{URL: "file:///srv/a.git", Credential: "tok"}).auth(); a != nil {
		t.Errorf("a local remote must not carry a credential, got %v", a)
	}
	a := (gitRemote{URL: "https://example.com/a.git", Credential: "tok"}).auth()
	if a == nil || !strings.Contains(a.String(), "http-basic-auth") {
		t.Errorf("auth = %v, want http basic auth", a)
	}
}

// TestGitRemoteBranchRef: an unnamed branch falls back to the default rather than
// discovering the remote's HEAD — naming it is what makes "the branch moved" a
// sentence an operator can act on.
func TestGitRemoteBranchRef(t *testing.T) {
	if got := (gitRemote{}).branchRef().String(); got != "refs/heads/"+defaultGitBranch {
		t.Errorf("default branch ref = %q", got)
	}
	if got := (gitRemote{Branch: "release"}).branchRef().String(); got != "refs/heads/release" {
		t.Errorf("named branch ref = %q", got)
	}
}

// TestGitAuthorNamesThePerson: history should name whoever pressed the button; an
// open server with no principal is signed by the server itself.
func TestGitAuthorNamesThePerson(t *testing.T) {
	now := time.Unix(1700000000, 0)
	sig := gitAuthor(&Principal{UserID: "u1", Username: "alice"}, now)
	if sig.Name != "alice" || !strings.HasPrefix(sig.Email, "alice@") || !sig.When.Equal(now) {
		t.Errorf("signature = %+v", sig)
	}
	anon := gitAuthor(nil, now)
	if anon.Name != "Atlas" || anon.Email == "" {
		t.Errorf("anonymous signature = %+v", anon)
	}
	// A principal with no username (a token-authenticated caller) is not a name.
	if got := gitAuthor(&Principal{UserID: "u2"}, now); got.Name != "Atlas" {
		t.Errorf("nameless principal signature = %+v", got)
	}
}

func TestShortHash(t *testing.T) {
	if got := short("0123456789abcdef"); got != "01234567" {
		t.Errorf("short = %q", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short of a short hash = %q", got)
	}
	if got := short(""); got != "" {
		t.Errorf("short of empty = %q", got)
	}
}

// TestGitDivergedErrorSaysWhatMoved: "it diverged" without naming the commits is
// not something an operator can act on.
func TestGitDivergedErrorSaysWhatMoved(t *testing.T) {
	moved := gitDivergedError{Expected: "aaaaaaaaaaaa", Actual: "bbbbbbbbbbbb"}
	msg := moved.Error()
	if !strings.Contains(msg, "aaaaaaaa") || !strings.Contains(msg, "bbbbbbbb") || !strings.Contains(msg, "import first") {
		t.Errorf("message = %q", msg)
	}
	unknown := gitDivergedError{Actual: "cccccccccccc"}
	if !strings.Contains(unknown.Error(), "never seen") {
		t.Errorf("message = %q", unknown.Error())
	}
}

// TestViewOfBindingHidesNothingButTheSecret: the view carries the credential's
// vault handle, which is a name, and never a resolved value — there is no field
// for one.
func TestViewOfBindingHidesNothingButTheSecret(t *testing.T) {
	rec := gitBinding{
		ApplicationID: "a1", RepoURL: "https://example.com/a.git", Branch: "main",
		CredentialRef: "git-token", LastCommit: "abc", LastSyncAt: 5, LastAction: "commit", BoundAt: 1,
	}
	v := viewOfBinding(rec, true)
	if !v.Bound || v.RepoURL != rec.RepoURL || v.CredentialRef != "git-token" || v.LastCommit != "abc" {
		t.Errorf("view = %+v", v)
	}
	if empty := viewOfBinding(rec, false); empty.Bound || empty.RepoURL != "" {
		t.Errorf("unbound view = %+v, want an empty one", empty)
	}
}

// TestAdoptOrMatchKey covers reconciling the application a binding names with the
// key the repository carries — the check that stops a bound import from filling a
// different application than the one the operator is looking at.
func TestAdoptOrMatchKey(t *testing.T) {
	t.Run("matching key", func(t *testing.T) {
		s := storesFor(t)
		if err := s.projects.save(project{ID: "a1", Name: "Onboarding", Key: "onboarding"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if mismatch, err := s.adoptOrMatchKey("a1", "onboarding"); mismatch != "" || err != nil {
			t.Fatalf("mismatch=%q err=%v", mismatch, err)
		}
	})

	t.Run("different key", func(t *testing.T) {
		s := storesFor(t)
		if err := s.projects.save(project{ID: "a1", Name: "Andere", Key: "andere"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		mismatch, err := s.adoptOrMatchKey("a1", "onboarding")
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(mismatch, "onboarding") || !strings.Contains(mismatch, "andere") {
			t.Errorf("mismatch = %q, want both keys named", mismatch)
		}
	})

	t.Run("keyless application adopts", func(t *testing.T) {
		s := storesFor(t)
		// An application saved before ADR-0134: no key at all.
		if err := s.projects.save(project{ID: "a1", Name: "Alt"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if mismatch, err := s.adoptOrMatchKey("a1", "onboarding"); mismatch != "" || err != nil {
			t.Fatalf("mismatch=%q err=%v", mismatch, err)
		}
		got, ok, err := s.projects.get("a1")
		if err != nil || !ok {
			t.Fatalf("get: %v ok=%v", err, ok)
		}
		if got.Key != "onboarding" {
			t.Errorf("key = %q, want the repository's adopted", got.Key)
		}
	})

	t.Run("adoption refused when the key is taken", func(t *testing.T) {
		s := storesFor(t)
		if err := s.projects.save(project{ID: "a1", Name: "Alt"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := s.projects.save(project{ID: "a2", Name: "Onboarding", Key: "onboarding"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		mismatch, err := s.adoptOrMatchKey("a1", "onboarding")
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(mismatch, "already uses the key") {
			t.Errorf("mismatch = %q", mismatch)
		}
		// And nothing was written: two applications must never share one identity.
		if got, _, _ := s.projects.get("a1"); got.Key != "" {
			t.Errorf("the key was adopted anyway: %q", got.Key)
		}
	})

	t.Run("unknown application", func(t *testing.T) {
		s := storesFor(t)
		_, err := s.adoptOrMatchKey("nope", "onboarding")
		var refusal sourceRefusal
		if !errors.As(err, &refusal) || refusal.status != 404 {
			t.Fatalf("err = %v, want a 404 refusal", err)
		}
	})
}

// TestReadSourceTreeFromCapsEntries: a repository must not be able to ask this
// server to hold an unbounded number of files.
func TestReadSourceTreeFromCapsEntries(t *testing.T) {
	fsys := memfs.New()
	if err := fsys.MkdirAll("processes", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i <= maxSourceEntries; i++ {
		f, err := fsys.Create("processes/p" + strconv.Itoa(i) + ".bpmn")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	if _, err := readSourceTreeFrom(fsys); err == nil ||
		!strings.Contains(err.Error(), "too many source files") {
		t.Fatalf("err = %v, want a too-many-files refusal", err)
	}
}

// TestGitBindingStoreReportsAnUnreadableRecord: a record that cannot be read is an
// error, not a silently absent binding — "not bound" and "cannot tell" are
// different answers.
func TestGitBindingStoreReportsAnUnreadableRecord(t *testing.T) {
	store, err := newGitBindingStore(filepath.Join(t.TempDir(), "git-bindings"))
	if err != nil {
		t.Fatalf("newGitBindingStore: %v", err)
	}
	// A directory where the record file belongs: reading it fails with something
	// other than "does not exist".
	if err := os.MkdirAll(store.fileFor("a1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok, err := store.get("a1"); err == nil || ok {
		t.Fatalf("get = ok %v, err %v; want an error", ok, err)
	}
}

// TestAdoptOrMatchKeyReportsStoreFailures: the key reconciliation reads and writes
// the project store, and a failure in either has to surface — silently skipping it
// would let an import land in an application whose identity was never recorded.
func TestAdoptOrMatchKeyReportsStoreFailures(t *testing.T) {
	// Unreadable record: "cannot tell" is not "no such application".
	s := storesFor(t)
	if err := os.MkdirAll(s.projects.fileFor("a1"), 0o755); err != nil {
		t.Fatalf("mkdir over record: %v", err)
	}
	if _, err := s.adoptOrMatchKey("a1", "app"); err == nil {
		t.Error("an unreadable record was reported as fine")
	}

	// The uniqueness scan fails: adopting a key that might already be taken is
	// exactly what must not happen on a guess.
	scan := storesFor(t)
	if err := scan.projects.save(project{ID: "a1", Name: "Alt"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scan.projects.dir, "6161.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := scan.adoptOrMatchKey("a1", "app"); err == nil {
		t.Error("a failed uniqueness scan was reported as fine")
	}
}

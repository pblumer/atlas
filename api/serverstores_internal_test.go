package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// A server's data directory holds one subdirectory per store, and New opens every one
// of them before it serves anything. Each open is followed by the same three lines —
// `if err != nil { return nil, err }` — repeated two dozen times, and not one of them
// had a test behind it.
//
// The property they carry is a startup property, not a plumbing detail: a data
// directory the server cannot open is reported *now*, while an operator is watching
// the process start, rather than at the first request that happens to touch that
// store. A store whose error was dropped would leave a server that starts, answers
// most requests, and fails one endpoint with a nil dereference.
//
// Occupying the store's path with a *file* is the cheapest real version of that: every
// store creates its directory, and MkdirAll over an existing file fails with ENOTDIR.
// It is also a shape that happens — a restore that unpacked a tarball wrongly, a
// volume mounted one level too deep.

// serverStoreDirs are the data-directory subdirectories New opens a store in, in the
// order it opens them. A store added to New without a line here is not a failure this
// test can see, which is the same gap that let two dozen of these go untested.
var serverStoreDirs = []string{
	"deployments", "jobtypes", "drafts", "playground-scenarios", "forms",
	"public-links", "projects", "process-docs", "panorama-models",
	"information-models", "releases", "grant-audit", "api-tokens", "deploy-tokens",
	"oauth-clients", "oauth-grants", "targets", "dmnrefs", "users", "groups",
	"connectors", "call-overrides", "repository", "inbound-subscriptions", "settings",
}

// newServerAtDir is New with the engine and log a server needs, over a data directory
// the caller has already arranged.
func newServerAtDir(t *testing.T, dir string) (*Server, error) {
	t.Helper()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return New(proc, store, dir)
}

// Every store New opens is one an unusable path stops the server over.
func TestNewRefusesToStartOnAStoreItCannotOpen(t *testing.T) {
	for _, sub := range serverStoreDirs {
		t.Run(sub, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, sub), []byte("not a directory"), 0o600); err != nil {
				t.Fatalf("occupy %s: %v", sub, err)
			}
			srv, err := newServerAtDir(t, dir)
			if err == nil {
				srv.Close()
				t.Fatalf("New started with %q occupied by a file: the store's error was dropped, "+
					"so the endpoints that read it fail later instead of the process failing now", sub)
			}
			// The message has to name the path. "mkdir: not a directory" with no
			// subject sends an operator looking through two dozen stores by hand.
			if !strings.Contains(err.Error(), sub) {
				t.Errorf("New: %v, want the message to name %q — it is what an operator has to fix", err, sub)
			}
		})
	}
}

// The same directory with nothing in its way starts. Without this the test above
// passes just as well against a New that refuses every data directory.
func TestNewStartsOnAnEmptyDataDirectory(t *testing.T) {
	srv, err := newServerAtDir(t, t.TempDir())
	if err != nil {
		t.Fatalf("New on an empty data directory: %v", err)
	}
	srv.Close()
}

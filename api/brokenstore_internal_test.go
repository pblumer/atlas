package api

import (
	"fmt"
	"os"
)

// dirStore is any design-time store — a bare sidecar.Store or one of the wrappers
// that embeds it — reduced to the one thing brokenStore needs.
type dirStore interface{ Dir() string }

// brokenStore takes a store constructor's results and removes the directory
// underneath the store, so every operation on it fails. Handler tests install one
// on a Server to reach their storage-failure (500) paths without a fake:
//
//	srv.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
//
// A store whose directory has been removed behaves exactly like one that was
// never created — a read is a clean miss, a listing, a write and a delete all
// fail — which is what makes this a faithful stand-in for a broken data
// directory.
//
// It panics rather than taking a *testing.T because Go cannot pass a
// multiple-value call through alongside another argument, and because neither
// failure is reachable in a test: the constructor is creating a directory under
// t.TempDir(), and the removal that follows is of a directory it just made.
func brokenStore[S dirStore](s S, err error) S {
	if err != nil {
		panic(fmt.Sprintf("brokenStore: build: %v", err))
	}
	if err := os.RemoveAll(s.Dir()); err != nil {
		panic(fmt.Sprintf("brokenStore: remove dir: %v", err))
	}
	return s
}

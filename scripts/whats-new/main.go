// Command whats-new regenerates the Console's "What's New" feed
// (api/web/whats-new.json) from CHANGELOG.md and the overrides beside it.
//
//	go run ./scripts/whats-new          (or: make whats-new)
//
// The work is in the feed package next to it, so the guards can call it directly
// rather than shelling out to a process and parsing what it printed.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/scripts/whats-new/feed"
)

func main() {
	// --root points the generator at another tree. It exists so a guard can run it
	// against a deliberately broken CHANGELOG without breaking this one; the default
	// is the repository this command lives in, which is what every real run uses.
	root := flag.String("root", "", "repository root (default: the one this command is in)")
	flag.Parse()

	dir := *root
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fail(err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fail(err)
	}
	paths := feed.PathsUnder(abs)
	n, err := feed.Write(paths)
	if err != nil {
		fail(err)
	}
	fmt.Printf("whats-new: wrote %d entries to %s\n", n, paths.Out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "whats-new: "+err.Error())
	os.Exit(1)
}

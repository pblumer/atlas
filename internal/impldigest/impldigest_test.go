package impldigest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/internal/impldigest"
)

// A small module: a imports b, b embeds a file and has a Windows-only file, c is
// imported by nobody, and a has a test file.
var moduleFiles = map[string]string{
	"go.mod":         "module example.com/m\n\ngo 1.26\n",
	"a/a.go":         "package a\n\nimport \"example.com/m/b\"\n\nvar X = b.Y\n",
	"a/a_test.go":    "package a\n",
	"b/b.go":         "package b\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Y string\n",
	"b/b_windows.go": "package b\n\nconst onWindows = true\n",
	"b/data.txt":     "payload\n",
	"c/c.go":         "package c\n",
}

func writeModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range moduleFiles {
		write(t, dir, name, body)
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compute(t *testing.T, dir string, roots ...string) impldigest.Result {
	t.Helper()
	res, err := impldigest.Compute(context.Background(), dir, roots...)
	if err != nil {
		t.Fatalf("Compute(%v): %v", roots, err)
	}
	return res
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestTheManifestNamesEveryFileTheRootCompilesFrom: the module's packages the root
// depends on, with their embedded files and their files for other platforms, plus
// go.mod; not a test file and not a package outside the closure. Each line is
// sha256sum's own format, so `sha256sum -c` checks it, and the digest is the
// SHA-256 of the manifest.
func TestTheManifestNamesEveryFileTheRootCompilesFrom(t *testing.T) {
	dir := writeModule(t)
	res := compute(t, dir, "example.com/m/a")
	var want strings.Builder
	for _, name := range []string{"a/a.go", "b/b.go", "b/b_windows.go", "b/data.txt", "go.mod"} {
		want.WriteString(sum(moduleFiles[name]) + "  " + name + "\n")
	}
	if string(res.Manifest) != want.String() {
		t.Fatalf("manifest =\n%s\nwant\n%s", res.Manifest, want.String())
	}
	if res.Digest != "sha256:"+sum(want.String()) {
		t.Fatalf("digest = %s, want the SHA-256 of the manifest", res.Digest)
	}
	if again := compute(t, dir, "example.com/m/a"); again.Digest != res.Digest {
		t.Fatalf("a second run gave %s, want %s", again.Digest, res.Digest)
	}
}

// TestOnlyWhatTheRootCompilesFromChangesTheDigest: a change anywhere in the closure
// invalidates it, a change outside it does not.
func TestOnlyWhatTheRootCompilesFromChangesTheDigest(t *testing.T) {
	for _, tc := range []struct {
		file, body string
		changes    bool
	}{
		{"b/b.go", "package b\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Y string\n\nvar Z = 1\n", true},
		{"b/data.txt", "another payload\n", true},
		{"b/b_windows.go", "package b\n\nconst onWindows = false\n", true},
		{"go.mod", "module example.com/m\n\ngo 1.25\n", true},
		{"go.sum", "example.org/x v1.0.0 h1:abc=\n", true},
		{"c/c.go", "package c\n\nvar Unused = 1\n", false},
		{"a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestX(*testing.T) {}\n", false},
		{"README.md", "# m\n", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			dir := writeModule(t)
			before := compute(t, dir, "example.com/m/a")
			write(t, dir, tc.file, tc.body)
			after := compute(t, dir, "example.com/m/a")
			if changed := after.Digest != before.Digest; changed != tc.changes {
				t.Fatalf("changing %s: digest changed = %v, want %v\n%s", tc.file, changed, tc.changes, after.Manifest)
			}
		})
	}
}

// TestTheReferencePlatformIsFixed: the closure is resolved for linux/amd64 without
// cgo and without the caller's build flags, so the digest is the same on every
// machine that computes it.
func TestTheReferencePlatformIsFixed(t *testing.T) {
	dir := writeModule(t)
	want := compute(t, dir, "example.com/m/a")
	t.Setenv("GOOS", "windows")
	t.Setenv("GOARCH", "arm64")
	t.Setenv("GOFLAGS", "-tags=nothing")
	t.Setenv("CGO_ENABLED", "1")
	if got := compute(t, dir, "example.com/m/a"); got.Digest != want.Digest {
		t.Fatalf("digest under another platform = %s, want %s", got.Digest, want.Digest)
	}
}

func TestComputeRefusesWhatItCannotName(t *testing.T) {
	dir := writeModule(t)
	for name, roots := range map[string][]string{
		"no root":                   {},
		"a root outside the module": {"strings"},
		"a missing package":         {"example.com/m/nope"},
	} {
		if _, err := impldigest.Compute(context.Background(), dir, roots...); err == nil {
			t.Errorf("%s: a digest was computed", name)
		}
	}
	if _, err := impldigest.Compute(context.Background(), t.TempDir(), "example.com/m/a"); err == nil {
		t.Error("a directory without go.mod got a digest")
	}
}

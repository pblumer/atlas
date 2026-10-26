// Package impldigest names the implementation of a built-in Worker Type by the
// bytes it is compiled from, so that conformance evidence can be bound to it
// (ADR-0483, "Digests and implementation identity").
//
// Built-in Worker Types all carry the version 1.0.0, and a commit cannot be named
// by evidence kept inside that same commit, so neither identifies what was tested.
// What does is the source: every file the module's packages in the root's
// dependency closure are built from — their Go files for every platform except
// tests, the files they embed — together with go.mod and go.sum. go.mod carries
// the Go version the module declares; the patch release that happens to run is
// left out on purpose, because CI and contributors differ in it and the digest
// must not.
//
// The closure is resolved for one reference platform, linux/amd64 without cgo,
// and without the caller's build flags, so the digest is the same wherever it is
// computed. This is build and review tooling: it runs the go command and is never
// called by the engine.
package impldigest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// The platform the closure is resolved for.
const (
	ReferenceGOOS   = "linux"
	ReferenceGOARCH = "amd64"
)

// Result is a digest and the manifest it is the SHA-256 of. The manifest has one
// line per file in sha256sum's format, "<hex>  <path>", sorted by path, the paths
// relative to the module root with forward slashes; `sha256sum -c` checks it, and
// comparing two manifests shows which file moved a digest.
type Result struct {
	Digest   string
	Manifest []byte
}

// listed is the part of `go list -json` this package reads.
type listed struct {
	ImportPath     string
	Dir            string
	Module         *struct{ Main bool }
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	EmbedFiles     []string
}

// Compute digests the module at moduleDir for the given root packages. A root that
// does not resolve, or roots that reach no package of the module, are an error:
// an implementation that cannot be named gets no digest.
func Compute(ctx context.Context, moduleDir string, roots ...string) (Result, error) {
	if len(roots) == 0 {
		return Result{}, errors.New("no root package to digest")
	}
	moduleDir, err := filepath.Abs(moduleDir)
	if err != nil {
		return Result{}, err
	}
	args := append([]string{"list", "-deps", "-json=ImportPath,Dir,Module,GoFiles,CgoFiles,IgnoredGoFiles,EmbedFiles", "--"}, roots...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOOS="+ReferenceGOOS, "GOARCH="+ReferenceGOARCH,
		"CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("go list %s: %w: %s", strings.Join(roots, " "), err, strings.TrimSpace(stderr.String()))
	}

	files := map[string]bool{"go.mod": true}
	if _, err := os.Stat(filepath.Join(moduleDir, "go.sum")); err == nil {
		files["go.sum"] = true
	}
	packages := 0
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Result{}, fmt.Errorf("read go list output: %w", err)
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		packages++
		for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.IgnoredGoFiles, p.EmbedFiles} {
			for _, name := range group {
				if strings.HasSuffix(name, "_test.go") {
					continue
				}
				rel, err := filepath.Rel(moduleDir, filepath.Join(p.Dir, name))
				if err != nil || strings.HasPrefix(rel, "..") {
					return Result{}, fmt.Errorf("package %s names %s outside the module", p.ImportPath, name)
				}
				files[filepath.ToSlash(rel)] = true
			}
		}
	}
	if packages == 0 {
		return Result{}, fmt.Errorf("%s reach no package of the module", strings.Join(roots, " "))
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var manifest bytes.Buffer
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(moduleDir, filepath.FromSlash(name)))
		if err != nil {
			return Result{}, err
		}
		h := sha256.Sum256(data)
		fmt.Fprintf(&manifest, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	h := sha256.Sum256(manifest.Bytes())
	return Result{Digest: "sha256:" + hex.EncodeToString(h[:]), Manifest: manifest.Bytes()}, nil
}

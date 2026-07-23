package dmn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirResolverResolvesAndMisses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "risk.dmn"), []byte("<dmn/>"), 0o644); err != nil {
		t.Fatalf("write .dmn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pricing.xml"), []byte("<xml/>"), 0o644); err != nil {
		t.Fatalf("write .xml: %v", err)
	}
	r := DirResolver{Dir: dir}
	ctx := context.Background()

	if got, err := r.Resolve(ctx, "risk"); err != nil || string(got) != "<dmn/>" {
		t.Fatalf("resolve risk = (%q, %v), want the .dmn bytes", got, err)
	}
	if got, err := r.Resolve(ctx, "pricing"); err != nil || string(got) != "<xml/>" {
		t.Fatalf("resolve pricing = (%q, %v), want the .xml fallback", got, err)
	}
	if _, err := r.Resolve(ctx, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve absent err = %v, want ErrNotFound", err)
	}
}

func TestDirResolverRejectsUnsafeHandles(t *testing.T) {
	r := DirResolver{Dir: t.TempDir()}
	for _, ref := range []string{"", "  ", ".", "..", "../secret", "sub/model", `a\b`} {
		if _, err := r.Resolve(context.Background(), ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("resolve %q err = %v, want ErrNotFound (rejected)", ref, err)
		}
	}
}

func TestDirResolverIOErrorIsNotNotFound(t *testing.T) {
	dir := t.TempDir()
	// A directory where the model file is expected makes ReadFile fail with a
	// non-not-exist error, which must surface (not be masked as ErrNotFound).
	if err := os.MkdirAll(filepath.Join(dir, "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := DirResolver{Dir: dir}.Resolve(context.Background(), "busy")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve over a dir-record err = %v, want a real I/O error", err)
	}
}

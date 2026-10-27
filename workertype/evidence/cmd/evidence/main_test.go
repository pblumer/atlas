package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The module root, seen from this package's directory.
const moduleDir = "../../../.."

func evidenceCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), moduleDir, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDigestPrintsWhatARecordWouldCarry(t *testing.T) {
	code, out, errOut := evidenceCmd(t, "digest", "github.com/pblumer/atlas/internal/dirsync")
	if code != 0 || !regexp.MustCompile(`^sha256:[0-9a-f]{64}\n$`).MatchString(out) {
		t.Fatalf("digest = %d %q %q", code, out, errOut)
	}
	code, manifest, _ := evidenceCmd(t, "digest", "-manifest", "github.com/pblumer/atlas/internal/dirsync")
	if code != 0 || !strings.Contains(manifest, "  internal/dirsync/") || !strings.HasSuffix(manifest, out) {
		t.Fatalf("digest -manifest = %d %q", code, manifest)
	}
}

func TestVerifyWithoutRecordsIsCurrent(t *testing.T) {
	if code, out, errOut := evidenceCmd(t, "verify"); code != 0 || out != "every record is current\n" {
		t.Fatalf("verify = %d %q %q", code, out, errOut)
	}
}

func TestEveryRefusalSaysWhy(t *testing.T) {
	dir := t.TempDir()
	notARecord := filepath.Join(dir, "x.json")
	if err := os.WriteFile(notARecord, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no command":              nil,
		"an unknown command":      {"publish"},
		"digest of nothing":       {"digest"},
		"digest of a non-package": {"digest", "github.com/pblumer/atlas/no/such"},
		"verify a missing file":   {"verify", filepath.Join(dir, "none.json")},
		"verify a non-record":     {"verify", notARecord},
		"renew nothing":           {"renew"},
		"renew a missing file":    {"renew", filepath.Join(dir, "none.json")},
		"renew a non-record":      {"renew", notARecord},
		"create without a path":   {"create", "-suite", "x"},
		"create over a record":    {"create", "-suite", "x", notARecord},
		"create unbindable":       {"create", "-contract", "workertype/contracts/x.json", "-worker-type", "atlas.entra", "-operation", "create-user", "-suite", "none", filepath.Join(dir, "new.json")},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, errOut := evidenceCmd(t, args...)
			if code == 0 || !strings.HasPrefix(errOut, "evidence: ") {
				t.Fatalf("%v = %d %q", args, code, errOut)
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), dir, []string{"verify"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "module root") {
		t.Fatalf("outside the module root = %d %q", code, stderr.String())
	}
}

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The edges of `atlas export`: what it refuses in an answer it did not expect, and
// in a directory it did not write.

func tarGz(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("header: %v", err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestTheSourceArchiveIsReadAsData(t *testing.T) {
	if _, err := unpackSource([]byte("plain")); err == nil || !strings.Contains(err.Error(), "not a gzip stream") {
		t.Errorf("not gzip = %v", err)
	}
	var corrupt bytes.Buffer
	gz := gzip.NewWriter(&corrupt)
	_, _ = gz.Write([]byte("not a tar header at all, but long enough to be read as one............................................................................................................................................................................................................................................................................................................................................................................................................................................................................................"))
	_ = gz.Close()
	if _, err := unpackSource(corrupt.Bytes()); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("corrupt = %v", err)
	}
	escape := tarGz(t, tar.Header{Name: "../outside.bpmn", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
	if _, err := unpackSource(escape); err == nil || !strings.Contains(err.Error(), "outside the package") {
		t.Errorf("a path outside = %v", err)
	}
	files, err := unpackSource(tarGz(t,
		tar.Header{Name: "processes/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "./atlas.json", Typeflag: tar.TypeReg, Size: 2, Mode: 0o644}))
	if err != nil || len(files) != 1 || files[0].name != "atlas.json" {
		t.Errorf("a directory entry and a ./ path = %v, %v", files, err)
	}
	many := make([]tar.Header, maxExportEntries+1)
	for i := range many {
		many[i] = tar.Header{Name: "f" + strings.Repeat("x", i%7) + string(rune('a'+i%26)), Typeflag: tar.TypeReg, Mode: 0o644}
	}
	if _, err := unpackSource(tarGz(t, many...)); err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Errorf("too many entries = %v", err)
	}

	if _, err := exportManifestOf(nil); err == nil || !strings.Contains(err.Error(), "has no atlas.json") {
		t.Errorf("no manifest = %v", err)
	}
	if _, err := exportManifestOf([]sourceEntry{{name: packageManifest, data: []byte("{")}}); err == nil {
		t.Error("a manifest that is not JSON was read")
	}
}

func TestAnEarlierPackageMustReadWhole(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, packageManifest, "{")
	if _, err := readEarlierPackage(dir); err == nil || !strings.Contains(err.Error(), packageManifest) {
		t.Errorf("an unreadable manifest = %v", err)
	}
	writeFile(t, dir, packageManifest, `{"key":"k"}`)
	writeFile(t, dir, packageQuestions, "[")
	if _, err := readEarlierPackage(dir); err == nil || !strings.Contains(err.Error(), "mend or delete it") {
		t.Errorf("unreadable questions = %v", err)
	}
	if earlier, err := readEarlierPackage(filepath.Join(dir, "neu")); err != nil || earlier.files != nil {
		t.Errorf("a directory not there yet = %+v, %v", earlier, err)
	}
}

// TestARepeatedFlagKeepsEachValueOnce: --catalog given twice names one catalogue
// once, and an empty one is refused. The question names and texts are apppkg's.
func TestARepeatedFlagKeepsEachValueOnce(t *testing.T) {
	var r repeatedFlag
	if err := r.Set(" "); err == nil {
		t.Error("an empty --catalog was accepted")
	}
	if err := r.Set("a"); err != nil || r.Set("a") != nil || r.String() != "a" {
		t.Errorf("a repeated --catalog = %v", r)
	}
}

// TestExportSaysWhatTheServerRefused: a refusal is the server's own words, at
// whichever step it came.
func TestExportSaysWhatTheServerRefused(t *testing.T) {
	for _, path := range []string{"/api/v1/applications", "/api/v1/applications/a1/source", "/api/v1/applications/a1/constants"} {
		t.Run(path, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == path:
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":"modeler role required"}`))
				case r.URL.Path == "/api/v1/applications":
					_, _ = w.Write([]byte(`[{"id":"a1","name":"A","key":"a"}]`))
				case r.URL.Path == "/api/v1/applications/a1/source":
					archive, _ := packSource([]sourceEntry{{name: packageManifest, data: []byte(`{"key":"a","name":"A"}`)}})
					_, _ = w.Write(archive)
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer mock.Close()
			err := runExport([]string{"--server", mock.URL, "--application", "a", filepath.Join(t.TempDir(), "p")}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "403 Forbidden: modeler role required") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

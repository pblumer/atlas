package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoHandlerReadsARequestBodyWithoutACeiling keeps the rule where nothing else
// would notice it broken: a handler that reads r.Body directly works exactly as one
// that reads it against a budget, until somebody sends it a gigabyte. Nineteen had
// been written that way beside dozens that were not. Every read of a request body in
// this tree goes through a ceiling — httpapi.LimitBody, http.MaxBytesReader or an
// io.LimitReader — so that the next handler cannot be the twentieth.
func TestNoHandlerReadsARequestBodyWithoutACeiling(t *testing.T) {
	unbounded := regexp.MustCompile(`(json\.NewDecoder|io\.ReadAll)\(r\.Body\)`)
	seen := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "web" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		seen++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if unbounded.MatchString(line) {
				t.Errorf("%s:%d reads the request body with no ceiling; read httpapi.LimitBody(w, r, <budget>) instead", path, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the api tree: %v", err)
	}
	if seen < 100 {
		t.Fatalf("looked at %d files; the test is not running in the api directory", seen)
	}
}

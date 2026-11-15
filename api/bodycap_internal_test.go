package api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/workertype/install"
)

// How much of a request body this server will hold.
//
// Every body a handler reads is input somebody else chose the size of. The budgets in
// package limits say how large each kind may be, and the completeness test there
// (TestNoCeilingWithoutAName) holds every *bound* to a named budget — but it can only
// see a bound that is written down. A handler that reads r.Body with no bound at all
// is invisible to it, and nineteen did. These tests hold the other half: no body is
// read without a bound, and under every handler there is one more that does not
// depend on the handler having remembered.

// unboundedBodyRead matches a body handed whole to the two calls that read until the
// sender stops: json.NewDecoder(r.Body) and io.ReadAll(r.Body), on a request or on an
// answer from somewhere else alike.
var unboundedBodyRead = regexp.MustCompile(`(json\.NewDecoder|io\.ReadAll)\(\s*[\w.]+\.Body\s*\)`)

// TestNoBodyIsReadWithoutABound walks the production code of api/ and its
// sub-packages. A body read through io.LimitReader, http.MaxBytesReader or the httpapi
// helpers does not match; a new handler written the short way does, and nothing else
// would notice it: the route works, it simply holds whatever it is sent.
func TestNoBodyIsReadWithoutABound(t *testing.T) {
	seen := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "node_modules" {
				return fs.SkipDir
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
			if unboundedBodyRead.MatchString(line) {
				t.Errorf("%s:%d reads a body with no bound; read it through httpapi.DecodeJSON or httpapi.ReadBody "+
					"with a budget from limits.Limits (or an io.LimitReader / http.MaxBytesReader over one)",
					filepath.ToSlash(path), i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking api/: %v", err)
	}
	// A walk that visited nothing passes exactly like a tree with nothing to find.
	if seen < 200 {
		t.Fatalf("only %d source files were read; the test is not running in the api package directory", seen)
	}
}

// TestTheBodyCeilingAdmitsEveryBudget: the ceiling under every route must never be
// what refuses a body some route is allowed to take. So it is held against every
// budget an installation can configure, one at a time raised above all the others,
// and against the one body bound that is a package format's rule rather than a
// budget: an installed Worker Type.
func TestTheBodyCeilingAdmitsEveryBudget(t *testing.T) {
	def := (&Server{limits: limits.Default()}).requestBodyCeiling()
	if def < limits.Default().Archive {
		t.Errorf("the ceiling is %d, below the %d a restore may upload", def, limits.Default().Archive)
	}
	if worker := install.MaxArtifactBytes + install.MaxManifestBytes + 1<<20; def < int64(worker) {
		t.Errorf("the ceiling is %d, below the %d a Worker Type installation may upload", def, worker)
	}
	// And it is a ceiling: within a few megabytes of the largest budget, not a
	// number so large it bounds nothing.
	if slack := def - limits.Default().Archive; slack > 8<<20 {
		t.Errorf("the ceiling is %d bytes above the largest budget; it has stopped being one", slack)
	}

	const raised = int64(1) << 40
	typ := reflect.TypeOf(limits.Limits{})
	checked := 0
	for i := range typ.NumField() {
		if typ.Field(i).Type.Kind() != reflect.Int64 {
			continue // a count or a rate, never the size of a body
		}
		l := limits.Default()
		reflect.ValueOf(&l).Elem().Field(i).SetInt(raised)
		if got := (&Server{limits: l}).requestBodyCeiling(); got < raised {
			t.Errorf("with %s raised to %d the ceiling is %d: a body that budget allows would be cut off",
				typ.Field(i).Name, raised, got)
		}
		checked++
	}
	if checked < 10 {
		t.Fatalf("only %d budgets were checked; limits.Limits no longer has the shape this test reads", checked)
	}

	// A server built as a struct literal has the zero Limits; the ceiling must come
	// from the defaults then, not be zero and refuse every body.
	if got := (&Server{}).requestBodyCeiling(); got != def {
		t.Errorf("a server with no configured limits has ceiling %d, want the default %d", got, def)
	}
}

// TestCapBodiesStopsAReadAtTheCeiling: what the ceiling does to a handler that reads
// with no bound of its own — it reads the ceiling's worth and is told the body was
// too large, rather than reading for as long as the sender keeps sending.
func TestCapBodiesStopsAReadAtTheCeiling(t *testing.T) {
	var (
		read    int64
		readErr error
	)
	h := capBodies(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	}), 1024)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 1024))))
	if read != 1024 || readErr != nil {
		t.Errorf("a body of exactly the ceiling: read %d, err %v; want all of it", read, readErr)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 5000))))
	var tooLarge *http.MaxBytesError
	if read != 1024 || !errors.As(readErr, &tooLarge) {
		t.Errorf("a body over the ceiling: read %d, err %v; want 1024 bytes and a MaxBytesError", read, readErr)
	}

	// A request without a body is served as it was.
	served := false
	capBodies(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }), 1024).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if !served {
		t.Error("a request without a body did not reach its handler")
	}
}

// endless is a body that never ends: what a handler with no bound of its own would
// read from a sender who does not stop.
type endless struct{}

func (endless) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// TestTheServedHandlerIsUnderTheCeiling: the ceiling is no use as a function nobody
// calls. Through the handler the server actually serves, a route with no bound of its
// own — the MCP transport here, standing in for any handler that forgets — is cut off
// at the ceiling.
func TestTheServedHandlerIsUnderTheCeiling(t *testing.T) {
	var (
		read    int64
		readErr error
	)
	l := limits.Default()
	// Small budgets, so the body that proves the point is megabytes and not a
	// gigabyte: the ceiling is then the Worker Type installation's bound.
	l.Archive = l.AppBundle
	s := newServerWithOptions(t, WithLimits(l), WithMCP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	})))
	ceiling := s.requestBodyCeiling()

	req := httptest.NewRequest("POST", "/mcp", io.NopCloser(endless{}))
	req.ContentLength = -1
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)

	var tooLarge *http.MaxBytesError
	if read != ceiling || !errors.As(readErr, &tooLarge) {
		t.Errorf("a route with no bound of its own read %d bytes (err %v); want it stopped at the ceiling of %d",
			read, readErr, ceiling)
	}
}

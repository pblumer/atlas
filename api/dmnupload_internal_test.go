package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestSanitizeHandle covers the filename → handle normalization.
func TestSanitizeHandle(t *testing.T) {
	cases := map[string]string{
		"Risk Model.dmn": "risk-model",
		"dish.xml":       "dish",
		"a/b\\c":         "a-b-c",
		"--edges--":      "edges",
		"  ":             "",
		"Ünïcode":        "n-code",
	}
	for in, want := range cases {
		if got := sanitizeHandle(in); got != want {
			t.Errorf("sanitizeHandle(%q) = %q, want %q", in, got, want)
		}
	}
}

func uploadHarness(t *testing.T) (http.Handler, func(method, path, body, ctype string) (int, []byte)) {
	t.Helper()
	srv, _ := newValidateServer(t)
	h := srv.Handler()
	do := func(method, path, body, ctype string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			if ctype != "" {
				req.Header.Set("Content-Type", ctype)
			}
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	return h, do
}

// TestUploadDmnModel is the "pick a .dmn and use it" path end to end: a valid model
// is stored, its returned handle names a real reference, and the decision catalog
// then lists its decision — no server-side file placement or handle typing.
func TestUploadDmnModel(t *testing.T) {
	_, do := uploadHarness(t)

	code, b := do(http.MethodPost, "/api/v1/dmn-models?name=Risk%20Model.dmn", validDMNModel, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	var up struct {
		ModelRef  string   `json:"modelRef"`
		ModelName string   `json:"modelName"`
		Decisions []string `json:"decisions"`
	}
	if err := json.Unmarshal(b, &up); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if up.ModelRef != "risk-model" {
		t.Errorf("modelRef = %q, want risk-model", up.ModelRef)
	}
	if len(up.Decisions) != 1 || up.Decisions[0] != "Dish" {
		t.Errorf("decisions = %v, want [Dish]", up.Decisions)
	}

	// The stored model is now referenceable and its decision appears in the catalog.
	if code, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Risk","modelRef":"`+up.ModelRef+`"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create ref: %d %s", code, b)
	}
	_, cb := do(http.MethodGet, "/api/v1/decisions", "", "")
	if !strings.Contains(string(cb), `"id":"Dish"`) {
		t.Fatalf("decisions catalog missing the uploaded model: %s", cb)
	}

	// A second upload of the same filename does not clobber the first.
	_, b2 := do(http.MethodPost, "/api/v1/dmn-models?name=Risk%20Model.dmn", validDMNModel, "application/xml")
	var up2 struct {
		ModelRef string `json:"modelRef"`
	}
	_ = json.Unmarshal(b2, &up2)
	if up2.ModelRef == up.ModelRef {
		t.Errorf("second upload reused handle %q, want a distinct one", up2.ModelRef)
	}
}

// TestUploadDmnModelInvalid rejects a non-compiling model with a 400.
func TestUploadDmnModelInvalid(t *testing.T) {
	_, do := uploadHarness(t)
	if code, _ := do(http.MethodPost, "/api/v1/dmn-models?name=bad.dmn", "<not-dmn", "application/xml"); code != http.StatusBadRequest {
		t.Fatalf("upload of junk = %d, want 400", code)
	}
	if code, _ := do(http.MethodPost, "/api/v1/dmn-models", "", ""); code != http.StatusBadRequest {
		t.Fatalf("empty upload = %d, want 400", code)
	}
}

// TestUploadDmnModelRemoteSourceRefused refuses upload when a remote temis service
// is the model source (nothing local to write to).
func TestUploadDmnModelRemoteSourceRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.dmnResolver = dmn.ServiceResolver{BaseURL: "https://temis.example"}
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dmn-models", strings.NewReader(validDMNModel))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("upload with a remote model source = %d, want 409", rec.Code)
	}
}

// TestUploadDmnModelHandleFromModelName covers the handle fallback: with no source
// filename, the stored handle is derived from the model's own name.
func TestUploadDmnModelHandleFromModelName(t *testing.T) {
	_, do := uploadHarness(t)
	code, b := do(http.MethodPost, "/api/v1/dmn-models", validDMNModel, "application/xml") // no ?name
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	var up struct {
		ModelRef string `json:"modelRef"`
	}
	_ = json.Unmarshal(b, &up)
	if !strings.HasPrefix(up.ModelRef, "dish") { // name-derived handle (dish-N, dish.dmn is pre-seeded)
		t.Errorf("modelRef = %q, want a dish-derived handle", up.ModelRef)
	}
}

// minimalDMN is a valid model whose name sanitizes to empty, to drive the "model"
// ultimate handle fallback.
const minimalDMN = `<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="###"><decision id="D" name="D"><literalExpression id="l"><text>1</text></literalExpression></decision></definitions>`

// TestUploadDmnModelDefaultHandle covers the fallback when neither the filename nor
// the model name yields any usable handle characters: the model is stored as
// "model".
func TestUploadDmnModelDefaultHandle(t *testing.T) {
	_, do := uploadHarness(t)
	code, b := do(http.MethodPost, "/api/v1/dmn-models?name=%23%23%23", minimalDMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, b)
	}
	var up struct {
		ModelRef string `json:"modelRef"`
	}
	_ = json.Unmarshal(b, &up)
	if up.ModelRef != "model" {
		t.Errorf("modelRef = %q, want the default \"model\"", up.ModelRef)
	}
}

// TestWriteUniqueModelDirError covers the store failure path: a directory that
// cannot be created (a file sits where a path component must be a directory).
func TestWriteUniqueModelDirError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := writeUniqueModel(filepath.Join(f, "sub"), "h", []byte("data")); err == nil {
		t.Fatal("writeUniqueModel under a file = nil error, want an error")
	}
}

// TestUploadDmnModelStoreError covers the endpoint's store-failure branch: a valid
// model whose target folder cannot be created is a 500.
func TestUploadDmnModelStoreError(t *testing.T) {
	srv, _ := newValidateServer(t)
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	srv.dmnResolver = dmn.DirResolver{Dir: filepath.Join(f, "models")} // a file sits at f
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dmn-models?name=x.dmn", strings.NewReader(validDMNModel))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("upload to an unwritable store = %d, want 500", rec.Code)
	}
}

// TestUploadDmnModelBodyError covers the read-body failure path.
func TestUploadDmnModelBodyError(t *testing.T) {
	srv, _ := newValidateServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dmn-models", errReader{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upload with a failing body = %d, want 400", rec.Code)
	}
}

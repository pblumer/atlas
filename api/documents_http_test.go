package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/limits"
)

// The document store over HTTP (ADR-draft-a-file-is-a-document-not-a-variable): a file
// is uploaded once, a variable holds the reference, the instance it enters binds it,
// and the CSV import parses the file from the store rather than from a variable.

const docCSV = "email,group\nada@example.ch,users\nbob@example.ch,ops\n"

type docRef struct {
	ID          string      `json:"$document"`
	Name        string      `json:"name"`
	ContentType string      `json:"contentType"`
	Size        json.Number `json:"size"`
	SHA256      string      `json:"sha256"`
}

func uploadDoc(t *testing.T, do func(method, path, body, ct string) (int, []byte), name, body string) docRef {
	t.Helper()
	code, out := do(http.MethodPost, "/api/v1/documents?name="+name, body, "text/csv")
	if code != http.StatusCreated {
		t.Fatalf("upload: status=%d body=%s", code, out)
	}
	var ref docRef
	if err := json.Unmarshal(out, &ref); err != nil || !strings.HasPrefix(ref.ID, "doc_") {
		t.Fatalf("upload answer %s: %v", out, err)
	}
	return ref
}

func docMeta(t *testing.T, do func(method, path, body, ct string) (int, []byte), id string) (int, map[string]any) {
	t.Helper()
	code, out := do(http.MethodGet, "/api/v1/documents/"+id+"/meta", "", "")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return code, m
}

func TestDocumentUploadAndDownload(t *testing.T) {
	ts := newTestServer(t)
	do := func(method, path, body, ct string) (int, []byte) { return doReq(t, ts, method, path, body, ct) }

	ref := uploadDoc(t, do, "mitarbeitende.csv", docCSV)
	if ref.Name != "mitarbeitende.csv" || ref.ContentType != "text/csv; charset=utf-8" || ref.Size.String() != fmt.Sprint(len(docCSV)) {
		t.Fatalf("reference = %+v", ref)
	}

	res, err := http.Get(ts.URL + "/api/v1/documents/" + ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != docCSV {
		t.Fatalf("download: %d %q", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.Header.Get("Content-Disposition"), "mitarbeitende.csv") {
		t.Errorf("headers = %v", res.Header)
	}

	code, meta := docMeta(t, do, ref.ID)
	if code != http.StatusOK || meta["state"] != "pending" || meta["available"] != true {
		t.Fatalf("meta = %d %v", code, meta)
	}

	// A page is downloaded, never shown as one.
	page := uploadDoc(t, do, "x.html", "<html><body><script>alert(1)</script></body></html>")
	res, _ = http.Get(ts.URL + "/api/v1/documents/" + page.ID)
	res.Body.Close()
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("html served with %v", res.Header)
	}

	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/api/v1/documents/doc_aaaaaaaaaaaaaaaaaaaaaaaaaa", "", http.StatusNotFound},
		{http.MethodGet, "/api/v1/documents/..%2Fmeta", "", http.StatusNotFound},
		{http.MethodPost, "/api/v1/documents?name=e.csv", "", http.StatusBadRequest},
	} {
		if code, out := do(tc.method, tc.path, tc.body, "text/csv"); code != tc.want {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, code, out, tc.want)
		}
	}
}

func TestDocumentUploadIsBoundedByTheDocumentLimit(t *testing.T) {
	l := limits.Default()
	l.Document = 16
	ts := newTestServerWith(t, api.WithLimits(l))
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/documents?name=a.csv", strings.Repeat("x", 16), "text/csv"); code != http.StatusCreated {
		t.Fatalf("at the limit: %d", code)
	}
	if code, out := doReq(t, ts, http.MethodPost, "/api/v1/documents?name=a.csv", strings.Repeat("x", 17), "text/csv"); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte over: %d %s", code, out)
	}
}

// csvDocBPMN reads the uploaded CSV with a CSV-to-JSON task and then waits, so the
// instance is there to be read afterwards.
const csvDocBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="csvdoc" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="read">
      <extensionElements>
        <atlas:csvConnector source="upload" delimiter="," hasHeader="true" columns="email,group" resultVariable="rows"/>
      </extensionElements>
    </serviceTask>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="read"/>
    <sequenceFlow id="f2" sourceRef="read" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

func deployDoc(t *testing.T, do func(method, path, body, ct string) (int, []byte), xml string) uint64 {
	t.Helper()
	code, out := do(http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, out)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(out, &dep)
	return dep.Key
}

func TestADocumentEntersAnInstanceAndIsParsedFromTheStore(t *testing.T) {
	ts := newTestServer(t)
	do := func(method, path, body, ct string) (int, []byte) { return doReq(t, ts, method, path, body, ct) }
	key := deployDoc(t, do, csvDocBPMN)
	ref := uploadDoc(t, do, "zeilen.csv", docCSV)

	refJSON, _ := json.Marshal(ref)
	code, out := do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key),
		`{"variables":{"upload":`+string(refJSON)+`}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, out)
	}
	var started struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(out, &started)

	// The CSV task parsed the file from the store: rows are there, and the variable
	// still holds only the reference.
	code, out = do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", started.InstanceKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d %s", code, out)
	}
	var vars map[string]any
	_ = json.Unmarshal(out, &vars)
	rows, _ := vars["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want two parsed from the document (variables %s)", vars["rows"], out)
	}
	if up, _ := vars["upload"].(map[string]any); up["$document"] != ref.ID {
		t.Fatalf("upload = %v, want the reference", vars["upload"])
	}
	if strings.Contains(string(out), "ada@example.ch,users") {
		t.Fatalf("the file's text is in the variables: %s", out)
	}

	// The document is bound to the instance it entered.
	if _, meta := docMeta(t, do, ref.ID); meta["state"] != "bound" {
		t.Fatalf("meta after start = %v, want bound", meta)
	}

	// A reference to a document that does not exist is refused before anything starts.
	code, out = do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key),
		`{"variables":{"upload":{"$document":"doc_aaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`, "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(out), "does not exist") {
		t.Fatalf("unknown document: %d %s", code, out)
	}
	// A message names no instance it could be bound to, so it may not carry one.
	code, out = do(http.MethodPost, "/api/v1/messages",
		`{"name":"x","correlationKey":"k","variables":{"upload":`+string(refJSON)+`}}`, "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(out), "document references") {
		t.Fatalf("message with a reference: %d %s", code, out)
	}
}

func TestADocumentIsOnlyItsUploadersUntilItEntersAnInstance(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "admin-password-1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "admin-password-1") != http.StatusOK {
		t.Fatal("admin login")
	}
	for _, u := range []string{"anna", "bert"} {
		if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
			`{"username":"`+u+`","password":"password-`+u+`-1","roles":["operator"]}`); code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", u, code, body)
		}
	}
	anna, bert := newClient(t), newClient(t)
	login(t, anna, ts, "anna", "password-anna-1")
	login(t, bert, ts, "bert", "password-bert-1")
	asAnna := func(method, path, body, ct string) (int, []byte) {
		return cReqTyped(t, anna, ts, method, path, ct, body)
	}
	asBert := func(method, path, body, ct string) (int, []byte) {
		return cReqTyped(t, bert, ts, method, path, ct, body)
	}

	ref := uploadDoc(t, asAnna, "a.csv", docCSV)
	if code, _ := asAnna(http.MethodGet, "/api/v1/documents/"+ref.ID, "", ""); code != http.StatusOK {
		t.Fatalf("the uploader cannot read their own upload: %d", code)
	}
	// Somebody else's pending upload is not there, as far as they can tell.
	if code, _ := asBert(http.MethodGet, "/api/v1/documents/"+ref.ID, "", ""); code != http.StatusNotFound {
		t.Fatalf("another account read a pending upload: %d", code)
	}
	asAdmin := func(method, path, body, ct string) (int, []byte) {
		return cReqTyped(t, admin, ts, method, path, ct, body)
	}
	key := deployDoc(t, asAdmin, csvDocBPMN)
	refJSON, _ := json.Marshal(ref)
	start := `{"variables":{"upload":` + string(refJSON) + `}}`
	// ...nor can they put it into an instance: the reference is a string anybody can type.
	if code, out := asBert(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), start, "application/json"); code != http.StatusForbidden {
		t.Fatalf("another account used a pending upload: %d %s", code, out)
	}
	if code, out := asAnna(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), start, "application/json"); code != http.StatusOK {
		t.Fatalf("the uploader could not use their upload: %d %s", code, out)
	}
	// Bound, it is readable by whoever may read the instance — an operator may.
	if code, _ := asBert(http.MethodGet, "/api/v1/documents/"+ref.ID, "", ""); code != http.StatusOK {
		t.Fatalf("an operator could not read a document of an instance they can read: %d", code)
	}
}

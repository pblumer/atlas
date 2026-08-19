package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildCSVUpload assembles a multipart/form-data body with an optional CSV file
// part and an optional JSON config part, returning the body and its content
// type. A nil argument omits that part, so error paths (missing file / missing
// config) are exercisable.
func buildCSVUpload(t *testing.T, fileName string, csv *string, config *string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if config != nil {
		if err := mw.WriteField("config", *config); err != nil {
			t.Fatalf("write config field: %v", err)
		}
	}
	if csv != nil {
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := io.WriteString(fw, *csv); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func postMultipart(t *testing.T, ts *httptest.Server, path string, body *bytes.Buffer, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func strptr(s string) *string { return &s }

const validCSVConfig = `{"columns":[
	{"name":"email","header":"E-Mail"},
	{"name":"group","header":"Gruppe"},
	{"name":"license","header":"Lizenz"}
]}`

const sampleRecordsCSV = "E-Mail,Gruppe,Lizenz\nada@x.io,admin,pro\nbob,ops,none\n"

// TestCreateInstanceFromCSV drives the whole ingestion path over HTTP: deploy a
// process, upload a CSV with a column layout, and confirm the parsed rows are
// seeded as the instance's start variables (ADR-0084).
func TestCreateInstanceFromCSV(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	upload, ct := buildCSVUpload(t, "records.csv", strptr(sampleRecordsCSV), strptr(validCSVConfig))
	path := fmt.Sprintf("/api/v1/processes/%d/instances-from-csv", deploy.Key)
	code, body = postMultipart(t, ts, path, upload, ct)
	if code != http.StatusOK {
		t.Fatalf("upload: status=%d body=%s", code, body)
	}
	var resp struct {
		DefinitionKey uint64 `json:"definitionKey"`
		RowCount      int    `json:"rowCount"`
		FileName      string `json:"fileName"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode resp: %v (%s)", err, body)
	}
	if resp.DefinitionKey != deploy.Key || resp.RowCount != 2 || resp.FileName != "records.csv" {
		t.Fatalf("resp = %+v, want key=%d rowCount=2 fileName=records.csv", resp, deploy.Key)
	}

	// Find the running instance and read its seeded variables back.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	var key uint64
	for _, in := range instances {
		if in.State == "active" {
			key = in.Key
		}
	}
	if key == 0 {
		t.Fatal("no active instance found")
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: status=%d body=%s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if got, ok := vars["rowCount"].(json.Number); !ok || got.String() != "2" {
		t.Errorf("rowCount = %v (%T), want number 2", vars["rowCount"], vars["rowCount"])
	}
	if got, ok := vars["fileName"].(string); !ok || got != "records.csv" {
		t.Errorf("fileName = %v, want records.csv", vars["fileName"])
	}
	rows, ok := vars["rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %v (%T), want an array of 2", vars["rows"], vars["rows"])
	}
	first, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("rows[0] = %v (%T), want an object", rows[0], rows[0])
	}
	if first["email"] != "ada@x.io" || first["group"] != "admin" || first["license"] != "pro" {
		t.Errorf("rows[0] = %v, want {email:ada@x.io, group:admin, license:pro}", first)
	}
}

// TestCreateInstanceFromCSVMalformedMultipart covers the ParseMultipartForm
// failure branch: a multipart content type with a body that is not a valid
// multipart document.
func TestCreateInstanceFromCSVMalformedMultipart(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	path := fmt.Sprintf("/api/v1/processes/%d/instances-from-csv", deploy.Key)
	code, body = doReq(t, ts, http.MethodPost, path, "not-a-multipart-body", "multipart/form-data; boundary=zzz")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", code, body)
	}
	if !strings.Contains(string(body), "parse upload") {
		t.Errorf("body = %s, want substring \"parse upload\"", body)
	}
}

func TestCreateInstanceFromCSVErrors(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	okPath := fmt.Sprintf("/api/v1/processes/%d/instances-from-csv", deploy.Key)

	// Deploy a non-executable process for the 409 path.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", nonExecutableBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy non-exec: status=%d body=%s", code, body)
	}
	var descr struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &descr); err != nil {
		t.Fatalf("decode non-exec deploy: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		fileName   string
		csv        *string
		config     *string
		wantStatus int
		wantSub    string
	}{
		{
			name: "missing file part", path: okPath, csv: nil, config: strptr(validCSVConfig),
			wantStatus: http.StatusBadRequest, wantSub: "missing file",
		},
		{
			name: "missing config part", path: okPath, csv: strptr(sampleRecordsCSV), config: nil,
			wantStatus: http.StatusBadRequest, wantSub: "missing config",
		},
		{
			name: "invalid config JSON", path: okPath, csv: strptr(sampleRecordsCSV), config: strptr("{"),
			wantStatus: http.StatusBadRequest, wantSub: "invalid config JSON",
		},
		{
			name: "column layout does not match file", path: okPath,
			csv:    strptr("email,team\nada@x.io,admin\n"),
			config: strptr(validCSVConfig),
			// header "E-Mail" is absent from this file
			wantStatus: http.StatusBadRequest, wantSub: "not found in CSV header",
		},
		{
			name: "unknown deployment key", path: "/api/v1/processes/99999/instances-from-csv",
			csv: strptr(sampleRecordsCSV), config: strptr(validCSVConfig),
			wantStatus: http.StatusNotFound, wantSub: "no deployment",
		},
		{
			name: "invalid definition key", path: "/api/v1/processes/not-a-number/instances-from-csv",
			csv: strptr(sampleRecordsCSV), config: strptr(validCSVConfig),
			wantStatus: http.StatusBadRequest, wantSub: "invalid definition key",
		},
		{
			name: "non-executable process", path: fmt.Sprintf("/api/v1/processes/%d/instances-from-csv", descr.Key),
			csv: strptr(sampleRecordsCSV), config: strptr(validCSVConfig),
			wantStatus: http.StatusConflict, wantSub: "not executable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fn := tc.fileName
			if fn == "" {
				fn = "records.csv"
			}
			upload, ct := buildCSVUpload(t, fn, tc.csv, tc.config)
			code, body := postMultipart(t, ts, tc.path, upload, ct)
			if code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", code, tc.wantStatus, body)
			}
			if !strings.Contains(string(body), tc.wantSub) {
				t.Errorf("body = %s, want substring %q", body, tc.wantSub)
			}
		})
	}
}

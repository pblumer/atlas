package examples

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The handbook's workshop chapter (api/web/handbuch.html, "Werkstatt: eine kleine
// Applikation bauen") ships the Bewerbermanagement application inside the page: a
// JSON block the "install" button reads to create the application, its forms, its
// decision and both processes in the reader's own instance, and that the two
// diagram cards render from.
//
// That block is a *second copy* of the files in this directory. A copy drifts:
// the model gets a fix here, the page keeps teaching the old one — and nothing
// notices, because the page has no compiler and no test of its own. This test is
// the generator and the guard in one. It builds the block from the files and
// compares it to what the page carries; `go test ./examples -update` writes the
// current one in.
//
// Deliberately one-directional: the files in this directory are the source, the
// page is the copy. Editing the block by hand is how the drift starts.
var update = flag.Bool("update", false, "rewrite the handbook's application data block from the files in this directory")

const (
	handbookPath = "../api/web/handbuch.html"
	dataOpen     = `<script type="application/json" id="bw-data">`
	dataClose    = `</script>`
)

// hbData is the shape the page's initWerkstatt() and its diagram cards read. Field
// order here is the field order in the file, so the generated block is stable.
type hbData struct {
	Application hbApplication `json:"application"`
	Decision    hbDecision    `json:"decision"`
	Forms       []hbForm      `json:"forms"`
	Processes   []hbProcess   `json:"processes"`
	Start       hbStart       `json:"start"`
}

type hbApplication struct {
	Name string `json:"name"`
	// Key is the portable application key the server derives from the name
	// (ADR-0134). The page looks an existing application up by it, so installing
	// twice reuses the first one instead of leaving an empty copy behind.
	Key string `json:"key"`
}

type hbDecision struct {
	// Handle is the model handle in the local DMN store, and the name the process
	// references as decisionId. The .dmn keeps <decision> id and name identical for
	// exactly this reason — Atlas resolves a decision by name.
	Handle string `json:"handle"`
	Name   string `json:"name"`
	XML    string `json:"xml"`
}

type hbForm struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

type hbProcess struct {
	ProcessID string `json:"processId"`
	XML       string `json:"xml"`
}

type hbStart struct {
	ProcessID string            `json:"processId"`
	Variables map[string]string `json:"variables"`
}

// handbookForms lists the forms in the order the page saves them, with the display
// name the Modeler shows. A form-js schema carries no name of its own, so it lives
// here rather than in a non-standard key inside the schema file.
var handbookForms = []struct{ file, id, name string }{
	{"bewerbung-eingang.form.json", "bw-bewerbung-eingang", "Bewerbung – Eingang"},
	{"interview-feedback.form.json", "bw-interview-feedback", "Interview – Feedback"},
	{"entscheidung.form.json", "bw-entscheidung", "Bewerbung – Entscheidung"},
}

// TestHandbookApplicationDataMatches keeps the page's embedded copy equal to the
// files here. Run with -update after changing a model, a form or the decision.
func TestHandbookApplicationDataMatches(t *testing.T) {
	want := handbookData(t)

	page, err := os.ReadFile(handbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", handbookPath, err)
	}
	start := bytes.Index(page, []byte(dataOpen))
	if start < 0 {
		t.Fatalf("%s has no %s block — the workshop chapter cannot install anything without it", handbookPath, dataOpen)
	}
	from := start + len(dataOpen)
	length := bytes.Index(page[from:], []byte(dataClose))
	if length < 0 {
		t.Fatalf("%s: the %s block is not closed", handbookPath, dataOpen)
	}
	got := string(page[from : from+length])

	if got == want {
		return
	}
	if *update {
		out := append([]byte{}, page[:from]...)
		out = append(out, want...)
		out = append(out, page[from+length:]...)
		if err := os.WriteFile(handbookPath, out, 0o644); err != nil {
			t.Fatalf("write %s: %v", handbookPath, err)
		}
		t.Logf("updated the application data block in %s (%d bytes)", handbookPath, len(want))
		return
	}
	t.Errorf("the handbook's application data block no longer matches examples/bewerbermanagement/ "+
		"(embedded %d bytes, files produce %d). The files are the source; run `go test ./examples -update` "+
		"and commit the regenerated %s.", len(got), len(want), handbookPath)
}

// handbookData builds the block: the artifacts, read verbatim, in the order the
// page uses them.
func handbookData(t *testing.T) string {
	t.Helper()
	data := hbData{
		Application: hbApplication{Name: "Bewerbermanagement", Key: "bewerbermanagement"},
		Decision: hbDecision{
			Handle: "bw-vorpruefung",
			Name:   "bw-vorpruefung",
			XML:    readArtifact(t, "vorpruefung.dmn"),
		},
		Processes: []hbProcess{
			{ProcessID: "bw-bewerbung", XML: readArtifact(t, "bewerbung.bpmn")},
			{ProcessID: "bw-interview", XML: readArtifact(t, "interview.bpmn")},
		},
		Start: hbStart{
			ProcessID: "bw-bewerbung",
			// abschluss and erfahrungJahre come from the page's two inputs; these
			// three are what every run shares.
			Variables: map[string]string{
				"name":   "Ada Lovelace",
				"email":  "ada@example.org",
				"stelle": "Software Engineer",
			},
		},
	}
	for _, f := range handbookForms {
		schema := readArtifact(t, f.file)
		if !json.Valid([]byte(schema)) {
			t.Fatalf("%s is not valid JSON", f.file)
		}
		data.Forms = append(data.Forms, hbForm{ID: f.id, Name: f.name, Schema: json.RawMessage(compactJSON(t, schema))})
	}
	// encoding/json escapes <, > and & as </>/&, so the block cannot
	// close its own <script> element — the same escaping the page's other embedded
	// models carry.
	out, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal application data: %v", err)
	}
	return string(out)
}

func readArtifact(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("bewerbermanagement", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	// Normalize the trailing newline away: it is a file convention, not content, and
	// keeping it out makes the embedded copy independent of how the file ends.
	return strings.TrimRight(string(b), "\n")
}

func compactJSON(t *testing.T, src string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(src)); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return buf.Bytes()
}

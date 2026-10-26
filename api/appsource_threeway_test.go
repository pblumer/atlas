package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The source import remembers what it brought (ADR-0467, amended): an
// artifact still as the last import left it is updated without a question when a
// newer tree brings it changed, one edited here is kept without a question when the
// tree brings it as before, and only one both sides moved is asked about.

// sourceTree is a tree for an application keyed "dreiweg" with one process, whose
// name says which version it is.
func sourceTree(t *testing.T, processName string, newline bool) []byte {
	t.Helper()
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p3" name="` + processName +
		`" isExecutable="true"><startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/></process></definitions>`
	if newline {
		xml += "\n"
	}
	return sourceArchive(t, [][2]string{
		{"atlas.json", `{"formatVersion":1,"key":"dreiweg","name":"Dreiweg","processes":[{"id":"p3","name":"Dreiweg","path":"processes/p3.bpmn"}]}`},
		{"processes/p3.bpmn", xml},
	})
}

func TestTheSourceImportTellsAnUpdateFromAnEdit(t *testing.T) {
	ts := newTestServer(t)
	v1, v2, v3 := sourceTree(t, "v1", true), sourceTree(t, "v2", true), sourceTree(t, "v3", true)
	if code, _, body := importSource(t, ts, v1); code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", code, body)
	}

	// Nobody touched it here: the newer tree is taken without a question.
	code, res, body := importSource(t, ts, v2)
	if code != http.StatusOK || res.outcomes()["process:p3"] != "behind/updated" {
		t.Fatalf("an untouched process and a newer tree: status=%d body=%s", code, body)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/p3/xml", "", ""); !strings.Contains(string(xml), `name="v2"`) {
		t.Fatalf("the newer version was not taken: %s", xml)
	}

	// Edited here, and the tree as before: kept without a question.
	editDraft(t, ts, res.ApplicationID, "hier")
	code, res, body = importSource(t, ts, v2)
	if code != http.StatusOK || res.outcomes()["process:p3"] != "ahead/kept" {
		t.Fatalf("an edited process and the same tree: status=%d body=%s", code, body)
	}

	// Edited here and newer in the tree: that one is asked.
	code, _, body = importSource(t, ts, v3)
	if code != http.StatusConflict || !strings.Contains(string(body), `"undecided":["process:p3"]`) {
		t.Fatalf("both sides moved: status=%d body=%s, want a 409 naming the process", code, body)
	}
	code, res, body = importSourceWith(t, ts, v3, "?keep=process:p3")
	if code != http.StatusOK || res.outcomes()["process:p3"] != "changed/kept" {
		t.Fatalf("keeping it: status=%d body=%s", code, body)
	}
	// The decision is remembered: the same tree again asks nothing.
	if code, res, body = importSource(t, ts, v3); code != http.StatusOK || res.outcomes()["process:p3"] != "ahead/kept" {
		t.Fatalf("the same tree after keeping: status=%d body=%s", code, body)
	}
	// An edit only here can still be replaced when somebody names it.
	if code, res, body = importSourceWith(t, ts, v3, "?overwrite=process:p3"); code != http.StatusOK || res.outcomes()["process:p3"] != "ahead/updated" {
		t.Fatalf("replacing an edit made here: status=%d body=%s", code, body)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/p3/xml", "", ""); !strings.Contains(string(xml), `name="v3"`) {
		t.Fatalf("the tree's version was not written: %s", xml)
	}
}

// editDraft saves the process p3 with another name, as somebody working on this
// server would.
func editDraft(t *testing.T, ts *httptest.Server, appID, name string) {
	t.Helper()
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p3" name="` + name +
		`" isExecutable="true"><startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/></process></definitions>`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID, xml, "application/xml"); code != http.StatusOK {
		t.Fatalf("edit draft status=%d body=%s", code, body)
	}
}

// TestATreeWithoutTrailingNewlinesImportsOnce: a hand-written file need not end in a
// newline, and the second import of it must still find nothing to do.
func TestATreeWithoutTrailingNewlinesImportsOnce(t *testing.T) {
	ts := newTestServer(t)
	tree := sourceTree(t, "v1", false)
	if code, _, body := importSource(t, ts, tree); code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", code, body)
	}
	code, res, body := importSource(t, ts, tree)
	if code != http.StatusOK || res.outcomes()["process:p3"] != "unchanged/unchanged" {
		t.Fatalf("the same tree again: status=%d body=%s", code, body)
	}
}

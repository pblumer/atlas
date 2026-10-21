package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The table of names a target keeps for the packages it imports (ADR-0467,
// amended 2026-10-21): what a package's name means here, chosen once and read before
// the directory, so the same question is not asked on every import.

type mappingView struct {
	Kind   string `json:"kind"`
	Answer string `json:"answer"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Here   bool   `json:"here"`
}

func listMappings(t *testing.T, do func(method, path, body string) (int, []byte)) []mappingView {
	t.Helper()
	code, body := do(http.MethodGet, "/api/v1/catalogs/import/mappings", "")
	var out []mappingView
	if code != http.StatusOK || json.Unmarshal(body, &out) != nil {
		t.Fatalf("list = %d %s", code, body)
	}
	return out
}

func TestTheTableOfNamesIsCheckedKeptAndRemoved(t *testing.T) {
	ts := newTestServer(t)
	do := func(method, path, body string) (int, []byte) {
		return doReq(t, ts, method, path, body, "application/json")
	}
	bau := createGroup(t, http.DefaultClient, ts.URL, "FB Bau")
	if code, body := do(http.MethodPost, "/api/v1/users", `{"username":"fmm","displayName":"Fabienne Meier-Muster","password":"a-long-password-1"}`); code != http.StatusCreated {
		t.Fatalf("create the account = %d %s", code, body)
	}

	for name, c := range map[string]struct {
		body string
		want int
	}{
		"a kind the table does not keep": {`{"mappings":[{"kind":"text","answer":"x","id":"y"}]}`, http.StatusBadRequest},
		"no name from the package":       {`{"mappings":[{"kind":"group","answer":" ","id":"` + bau + `"}]}`, http.StatusBadRequest},
		"a group this server lacks":      {`{"mappings":[{"kind":"group","answer":"Fachbereich Bau","id":"grp_nope"}]}`, http.StatusUnprocessableEntity},
	} {
		if code, body := do(http.MethodPost, "/api/v1/catalogs/import/mappings", c.body); code != c.want {
			t.Errorf("%s = %d %s, want %d", name, code, body, c.want)
		}
	}
	if got := listMappings(t, do); len(got) != 0 {
		t.Fatalf("a refused entry was written: %+v", got)
	}

	// An account is named by its username, whatever its case, and kept as spelt here.
	code, body := do(http.MethodPost, "/api/v1/catalogs/import/mappings",
		`{"mappings":[{"kind":"group","answer":"Fachbereich Bau","id":"`+bau+`"},{"kind":"account","answer":"Fabienne Meier","id":"FMM"}]}`)
	if code != http.StatusOK {
		t.Fatalf("set = %d %s", code, body)
	}
	got := listMappings(t, do)
	if len(got) != 2 || got[0] != (mappingView{Kind: "account", Answer: "Fabienne Meier", ID: "fmm", Name: "Fabienne Meier-Muster", Here: true}) ||
		got[1] != (mappingView{Kind: "group", Answer: "Fachbereich Bau", ID: bau, Name: "FB Bau", Here: true}) {
		t.Fatalf("table = %+v", got)
	}

	// The answers route reads it before the directory, and says so.
	doc := `{"catalogs":[{"id":"c","groups":["{{bau}}"]}],"products":[{"id":"p","approval":{"kind":"fixed","ref":"{{fm}}"}}]}`
	questions := `{"bau":{"kind":"group"},"fm":{"kind":"account"}}`
	code, body = do(http.MethodPost, "/api/v1/catalogs/import/answers",
		`{"document":`+doc+`,"questions":`+questions+`,"answers":{"bau":"Fachbereich Bau","fm":"Fabienne Meier"}}`)
	var res struct {
		Document   json.RawMessage `json:"document"`
		Remembered []string        `json:"remembered"`
		Learned    []mappingView   `json:"learned"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &res) != nil || strings.Join(res.Remembered, ",") != "bau,fm" || len(res.Learned) != 0 {
		t.Fatalf("answers with the table = %d %s", code, body)
	}
	if !strings.Contains(string(res.Document), `"groups":["`+bau+`"]`) || !strings.Contains(string(res.Document), `"ref":"fmm"`) {
		t.Fatalf("filled = %s", res.Document)
	}

	// Removed, the name is looked up again, and a choice made then is learned.
	if code, body := do(http.MethodDelete, "/api/v1/catalogs/import/mappings?kind=group&answer=Fachbereich%20Bau", ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, _ := do(http.MethodDelete, "/api/v1/catalogs/import/mappings?kind=group&answer=Fachbereich%20Bau", ""); code != http.StatusNotFound {
		t.Errorf("deleting it again = %d, want 404", code)
	}
	code, body = do(http.MethodPost, "/api/v1/catalogs/import/answers",
		`{"document":`+doc+`,"questions":`+questions+`,"answers":{"bau":"Fachbereich Bau","fm":"Fabienne Meier"},"chosen":{"bau":"`+bau+`"}}`)
	if code != http.StatusOK || json.Unmarshal(body, &res) != nil || len(res.Learned) != 1 ||
		res.Learned[0] != (mappingView{Kind: "group", Answer: "Fachbereich Bau", ID: bau, Name: "FB Bau"}) {
		t.Fatalf("a choice = %d %s, want it learned", code, body)
	}

	// A group deleted here leaves its entry in the table, marked as no longer here.
	if code, body := do(http.MethodPost, "/api/v1/catalogs/import/mappings", `{"mappings":[{"kind":"group","answer":"Fachbereich Bau","id":"`+bau+`"}]}`); code != http.StatusOK {
		t.Fatalf("set again = %d %s", code, body)
	}
	if code, body := do(http.MethodDelete, "/api/v1/groups/"+bau, ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete the group = %d %s", code, body)
	}
	for _, m := range listMappings(t, do) {
		if m.Kind == "group" && m.Here {
			t.Errorf("an entry for a deleted group reads as here: %+v", m)
		}
	}
}

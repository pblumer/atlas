package playground

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// generateBody is the description used across these tests: an amount, a weighted
// tier and a reference number — the three shapes a dataset is usually made of.
const generateBody = `{"generate":{"count":6,"fields":[
	{"name":"amount","kind":"int","min":100,"max":5000},
	{"name":"tier","kind":"choice","choices":[{"value":"gold","weight":1},{"value":"standard","weight":9}]},
	{"name":"ref","kind":"sequence","prefix":"ORDER-"}
]},"arrival":{"mode":"allAtOnce"}}`

// A dataset can be described instead of listed. That is what makes a run of three
// hundred cases something somebody writes down: nobody types three hundred
// amounts, and nobody reviews a pull request that contains them.
func TestARunIsDrivenByADescriptionOfItsData(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	if rec := call(t, svc.HandleStartRun, http.MethodPost, generateBody, vals); rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body %s", rec.Code, rec.Body)
	}
	st := waitForRun(t, svc, id, "finished")
	if st.Cases != 6 || st.Completed != 6 {
		t.Fatalf("status = %+v, want six cases all completed", st)
	}

	var res resultsResp
	decodeInto(t, call(t, svc.HandleResults, http.MethodGet, "", vals), &res)
	if res.Total != 6 {
		t.Fatalf("results total = %d, want 6", res.Total)
	}
	for i, row := range res.Rows {
		if row.Variables["amount"] == "" || row.Variables["tier"] == "" {
			t.Errorf("case %d carries %v, want the described fields", i, row.Variables)
		}
		if want := "ORDER-" + string(rune('1'+i)); row.Variables["ref"] != want {
			t.Errorf("case %d's ref = %q, want %q in arrival order", i, row.Variables["ref"], want)
		}
	}
}

// The preview is what the run will carry, not an illustration of it: the panel
// draws on the session's own seed and start, so the amounts on screen are the
// amounts that run.
func TestThePreviewShowsTheCasesTheRunWillCarry(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}

	var preview previewResp
	decodeInto(t, call(t, svc.HandleGeneratePreview, http.MethodPost,
		`{"count":6,"fields":[{"name":"amount","kind":"int","min":100,"max":5000},{"name":"ref","kind":"sequence","prefix":"ORDER-"}]}`,
		vals), &preview)
	if preview.Total != 6 {
		t.Errorf("total = %d, want the whole dataset's size, not the page's", preview.Total)
	}
	if got := preview.Columns; len(got) != 2 || got[0] != "amount" || got[1] != "ref" {
		t.Errorf("columns = %v, want them in the order the fields were described", got)
	}
	if len(preview.Rows) != 6 {
		t.Fatalf("rows = %d, want the six there are", len(preview.Rows))
	}

	call(t, svc.HandleStartRun, http.MethodPost, generateBody, vals)
	waitForRun(t, svc, id, "finished")
	var res resultsResp
	decodeInto(t, call(t, svc.HandleResults, http.MethodGet, "", vals), &res)
	for i, row := range res.Rows {
		want, _ := json.Marshal(preview.Rows[i]["amount"])
		if got := row.Variables["amount"]; got != strings.Trim(string(want), `"`) {
			t.Errorf("case %d ran with %s but the preview showed %s", i, got, want)
		}
	}
}

// The same description and the same seed produce the same run. Without that a
// generated scenario cannot be compared against a baseline, which is most of what
// a scenario is for.
func TestTheSameDescriptionAndSeedRunTheSameCases(t *testing.T) {
	svc := newService(t)
	first := resultsOfGeneratedRun(t, svc)
	second := resultsOfGeneratedRun(t, svc)
	if len(first) != len(second) {
		t.Fatalf("runs of %d and %d cases", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("case %d ran with %s the first time and %s the second", i, first[i], second[i])
		}
	}
}

// resultsOfGeneratedRun runs the shared description on a fresh session and
// returns each case's amount.
func resultsOfGeneratedRun(t *testing.T, svc *Service) []string {
	t.Helper()
	id := openBatchSession(t, svc)
	vals := map[string]string{"id": id}
	call(t, svc.HandleStartRun, http.MethodPost, generateBody, vals)
	waitForRun(t, svc, id, "finished")
	var res resultsResp
	decodeInto(t, call(t, svc.HandleResults, http.MethodGet, "", vals), &res)
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, row.Variables["amount"]+"/"+row.Variables["tier"])
	}
	call(t, svc.HandleClose, http.MethodDelete, "", vals)
	return out
}

// A request that both lists cases and describes them says two different things
// about what is about to run. Picking one silently is how a run ends up meaning
// something other than what was asked.
func TestARunIsNotDrivenByAListAndADescriptionAtOnce(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}
	rec := call(t, svc.HandleStartRun, http.MethodPost,
		`{"cases":[{"amount":1}],"generate":{"count":2,"fields":[{"name":"amount","kind":"int","max":9}]}}`, vals)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not by both") {
		t.Errorf("body %s does not say which two things it was given", rec.Body)
	}
}

// A description that cannot be run is refused before anything is drawn from it,
// and the refusal says what is wrong with it.
func TestADescriptionThatCannotRunIsRefused(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	for _, tc := range []struct {
		name, body, says string
	}{
		{"a kind that does not exist",
			`{"generate":{"count":2,"fields":[{"name":"n","kind":"lorem"}]}}`, "not one of"},
		{"a maximum below the minimum",
			`{"generate":{"count":2,"fields":[{"name":"n","kind":"int","min":9,"max":1}]}}`, "below its minimum"},
		{"no cases at all",
			`{"generate":{"count":0,"fields":[]}}`, "at least one case"},
		{"more cases than a run holds",
			`{"generate":{"count":50001,"fields":[]}}`, "at most 50000 cases"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, svc.HandleStartRun, http.MethodPost, tc.body, vals)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.says) {
				t.Errorf("body %s does not say %q", rec.Body, tc.says)
			}
		})
	}

	// The preview refuses the same descriptions, so a mistake is seen where it was
	// typed rather than when the run is started.
	if rec := call(t, svc.HandleGeneratePreview, http.MethodPost,
		`{"count":2,"fields":[{"name":"n","kind":"lorem"}]}`, vals); rec.Code != http.StatusBadRequest {
		t.Errorf("preview of a bad description = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if rec := call(t, svc.HandleGeneratePreview, http.MethodPost, `{not json`, vals); rec.Code != http.StatusBadRequest {
		t.Errorf("preview of a malformed body = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if rec := call(t, svc.HandleGeneratePreview, http.MethodPost, `{"count":1}`,
		map[string]string{"id": "nope"}); rec.Code != http.StatusNotFound {
		t.Errorf("preview on an unknown session = %d, want 404", rec.Code)
	}
}

// A preview is a sample somebody reads, so it is bounded however many rows the
// caller asks for.
func TestAPreviewIsBoundedHoweverManyAreAskedFor(t *testing.T) {
	svc := newService(t)
	id := openBatchSession(t, svc)
	body := `{"count":1000,"fields":[{"name":"n","kind":"int","min":0,"max":9}]}`

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 10},
		{"?limit=3", 3},
		{"?limit=1000", maxPreviewRows},
		{"?limit=nonsense", 10},
	} {
		r := httptest.NewRequest(http.MethodPost, "/"+tc.query, strings.NewReader(body))
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		svc.HandleGeneratePreview(rec, r)

		var out previewResp
		decodeInto(t, rec, &out)
		if len(out.Rows) != tc.want {
			t.Errorf("%q gave %d rows, want %d", tc.query, len(out.Rows), tc.want)
		}
		if out.Total != 1000 {
			t.Errorf("%q reported a total of %d, want the whole dataset's 1000", tc.query, out.Total)
		}
	}
}

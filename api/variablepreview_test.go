package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// previewVar is a variable as the listing reports it, with the preview fields.
type previewVar struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Kind      string `json:"kind"`
	Truncated bool   `json:"truncated"`
	Size      int    `json:"size"`
	Lines     int    `json:"lines"`
	Shape     string `json:"shape"`
	Count     int    `json:"count"`
}

// previewRows decodes a page of instance rows down to their variables, by name.
func previewRows(t *testing.T, body []byte) []map[string]previewVar {
	t.Helper()
	var rows []struct {
		Variables []previewVar `json:"variables"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode rows: %v (%s)", err, body)
	}
	out := make([]map[string]previewVar, len(rows))
	for i, r := range rows {
		out[i] = map[string]previewVar{}
		for _, v := range r.Variables {
			out[i][v.Name] = v
		}
	}
	return out
}

// TestInstanceListValuesPreview covers ?values=preview on the instance listing and the
// instance search: the Operations overview polls a page of instances every 1.5 seconds,
// and a variable holding a whole CSV upload made every poll carry that file once per
// listed instance. With the parameter, a long value comes back as its beginning, marked
// truncated, with what the full value weighs; without it, nothing changes for the
// callers that read the listing today.
func TestInstanceListValuesPreview(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", timerWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	lines := []string{"personalnummer;vorname;nachname;ort"}
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("P-%d;Anna;Müller;Zürich", 1000+i))
	}
	csv := strings.Join(lines, "\n")
	type row struct {
		Vorname string `json:"vorname"`
		Ort     string `json:"ort"`
	}
	rows := make([]row, 50)
	for i := range rows {
		rows[i] = row{Vorname: fmt.Sprintf("Person %d", i), Ort: "Zürich"}
	}
	vars, _ := json.Marshal(map[string]any{
		"variables": map[string]any{"csvText": csv, "rows": rows, "kurz": "abc", "n": 5},
	})
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), string(vars), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(body, &inst)

	list := fmt.Sprintf("/api/v1/instances?process=%d&state=active", dep.Key)

	// Without the parameter the listing is what it always was: every value whole.
	_, body = doReq(t, ts, http.MethodGet, list, "", "")
	full := previewRows(t, body)
	if len(full) != 1 {
		t.Fatalf("rows = %d, want 1", len(full))
	}
	if got := full[0]["csvText"]; got.Value != csv || got.Truncated || got.Size != 0 {
		t.Fatalf("full csvText = truncated %v size %d len %d, want the whole value unmarked", got.Truncated, got.Size, len(got.Value))
	}

	checkCSV := func(t *testing.T, vars map[string]previewVar) {
		t.Helper()
		c := vars["csvText"]
		if !c.Truncated {
			t.Fatalf("csvText not truncated: %+v", c)
		}
		if len(c.Value) >= len(csv) || len(c.Value) > 512 || !strings.HasPrefix(csv, c.Value) {
			t.Fatalf("csvText preview = %d bytes, want a prefix of at most 512 of %d", len(c.Value), len(csv))
		}
		if !utf8.ValidString(c.Value) {
			t.Fatalf("csvText preview cuts a character in half: %q", c.Value[len(c.Value)-4:])
		}
		if c.Size != len(csv) || c.Lines != len(lines) || c.Kind != "string" {
			t.Fatalf("csvText size=%d lines=%d kind=%q, want %d, %d, string", c.Size, c.Lines, c.Kind, len(csv), len(lines))
		}
	}
	check := func(t *testing.T, vars map[string]previewVar) {
		t.Helper()
		checkCSV(t, vars)
		r := vars["rows"]
		if !r.Truncated || r.Kind != "json" || r.Shape != "array" || r.Count != 50 || r.Size <= 512 {
			t.Fatalf("rows = truncated %v kind %q shape %q count %d size %d, want a truncated array of 50", r.Truncated, r.Kind, r.Shape, r.Count, r.Size)
		}
		// A short value is untouched and carries no preview fields.
		if k := vars["kurz"]; k.Value != "abc" || k.Truncated || k.Size != 0 {
			t.Fatalf("kurz = %+v, want it whole and unmarked", k)
		}
		if n := vars["n"]; n.Value != "5" || n.Truncated {
			t.Fatalf("n = %+v, want 5", n)
		}
	}

	t.Run("listing", func(t *testing.T) {
		_, body := doReq(t, ts, http.MethodGet, list+"&values=preview", "", "")
		got := previewRows(t, body)
		if len(got) != 1 {
			t.Fatalf("rows = %d, want 1", len(got))
		}
		check(t, got[0])
	})

	t.Run("search", func(t *testing.T) {
		// A content search answers with the variables that matched, so this one asks
		// for the long one.
		_, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=csvText%%3Dpersonalnummer*&values=preview", dep.Key), "", "")
		got := previewRows(t, body)
		if len(got) != 1 {
			t.Fatalf("search rows = %d, want 1 (%s)", len(got), body)
		}
		checkCSV(t, got[0])
		// The whole value is one point read away — which is how the console's value
		// window gets it.
		_, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?q=%d", inst.Key), "", "")
		whole := previewRows(t, body)
		if len(whole) != 1 || whole[0]["csvText"].Value != csv {
			t.Fatalf("point read did not return the whole value")
		}
	})

	t.Run("full is the explicit default", func(t *testing.T) {
		_, body := doReq(t, ts, http.MethodGet, list+"&values=full", "", "")
		if got := previewRows(t, body); got[0]["csvText"].Value != csv {
			t.Fatalf("values=full did not return the whole value")
		}
	})

	t.Run("an unknown mode is refused, not ignored", func(t *testing.T) {
		if code, _ := doReq(t, ts, http.MethodGet, list+"&values=short", "", ""); code != http.StatusBadRequest {
			t.Fatalf("listing values=short status=%d, want 400", code)
		}
		if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/search?q=x&values=short", "", ""); code != http.StatusBadRequest {
			t.Fatalf("search values=short status=%d, want 400", code)
		}
	})
}

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// versionComparison is the answer GET /api/v1/processes/{key}/comparison gives.
type versionComparison struct {
	ProcessID string `json:"processId"`
	From      struct {
		Key     uint64 `json:"key"`
		Version int32  `json:"version"`
	} `json:"from"`
	To struct {
		Key        uint64 `json:"key"`
		Version    int32  `json:"version"`
		DeployedAt int64  `json:"deployedAt"`
	} `json:"to"`
	SameModel  bool             `json:"sameModel"`
	Difference string           `json:"difference"`
	Changes    []comparedChange `json:"changes"`
	Counts     struct {
		Added   int `json:"added"`
		Removed int `json:"removed"`
		Changed int `json:"changed"`
	} `json:"counts"`
}

type comparedChange struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	Details []struct {
		Field  string `json:"field"`
		Kind   string `json:"kind"`
		Before string `json:"before"`
		After  string `json:"after"`
	} `json:"details"`
}

func getVersionComparison(t *testing.T, ts *httptest.Server, path string) versionComparison {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, path, "", "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	var c versionComparison
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode comparison: %v (%s)", err, body)
	}
	return c
}

// summarize renders a change list as one line per change, for comparing with a want.
func summarize(cs []comparedChange) string {
	var lines []string
	for _, c := range cs {
		line := c.Kind + " " + c.Type + " " + c.ID
		for _, d := range c.Details {
			line += fmt.Sprintf(" [%s %s: %q -> %q]", d.Kind, d.Field, d.Before, d.After)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// TestComparingTwoVersionsListsWhatChanged is the question a new version raises:
// what is different from the one before. Without ?with= the comparison is with the
// previous version; with it, with the one named, in either direction. Every element
// added, removed or changed is listed with what changed in it — not just the first
// difference, which is all the draft comparison used to say.
func TestComparingTwoVersionsListsWhatChanged(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployVersion(t, ts, incidentUserTaskBPMN)
	v2 := deployVersion(t, ts, strings.Replace(incidentUserTaskBPMN, `name="Review"`, `name="Review twice"`, 1))
	// v3 puts a second task between review and the end.
	v3XML := strings.NewReplacer(
		`name="Review"`, `name="Review twice"`,
		`<sequenceFlow id="f2" sourceRef="review" targetRef="end"/>`,
		`<userTask id="sign" name="Sign"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="sign"/>
    <sequenceFlow id="f3" sourceRef="sign" targetRef="end"/>`,
	).Replace(incidentUserTaskBPMN)
	v3 := deployVersion(t, ts, v3XML)

	c := getVersionComparison(t, ts, fmt.Sprintf("/api/v1/processes/%d/comparison", v3))
	if c.ProcessID != "approval" || c.From.Key != v2 || c.From.Version != 2 || c.To.Key != v3 || c.To.Version != 3 {
		t.Fatalf("comparison = %+v, want v2 (key %d) → v3 (key %d) of approval", c, v2, v3)
	}
	if c.SameModel || c.Difference == "" || c.To.DeployedAt == 0 {
		t.Fatalf("comparison = %+v, want two different models, the first difference and the deploy time", c)
	}
	// In v3's document order: the new task stands before the flow rewired to it.
	want := strings.Join([]string{
		`added userTask sign`,
		`changed sequenceFlow f2 [changed targetRef: "end" -> "sign"]`,
		`added sequenceFlow f3`,
	}, "\n")
	if got := summarize(c.Changes); got != want {
		t.Fatalf("v2 → v3 changes:\n%s\nwant\n%s", got, want)
	}
	if c.Counts.Added != 2 || c.Counts.Changed != 1 || c.Counts.Removed != 0 {
		t.Fatalf("counts = %+v, want 2 added, 1 changed", c.Counts)
	}

	// An older version named explicitly.
	c = getVersionComparison(t, ts, fmt.Sprintf("/api/v1/processes/%d/comparison?with=%d", v2, v1))
	want = `changed userTask review [changed name: "Review" -> "Review twice"]`
	if c.From.Version != 1 || summarize(c.Changes) != want {
		t.Fatalf("v1 → v2 changes:\n%s\nwant\n%s", summarize(c.Changes), want)
	}

	// The other direction reads the other way round: what v1 lacks against v3.
	c = getVersionComparison(t, ts, fmt.Sprintf("/api/v1/processes/%d/comparison?with=%d", v1, v3))
	if c.From.Version != 3 || c.To.Version != 1 || c.Counts.Removed != 2 {
		t.Fatalf("v3 → v1 = %+v, want sign and f3 removed", c)
	}

	// A version compared with itself is the same model and changes nothing.
	c = getVersionComparison(t, ts, fmt.Sprintf("/api/v1/processes/%d/comparison?with=%d", v2, v2))
	if !c.SameModel || c.Difference != "" || len(c.Changes) != 0 {
		t.Fatalf("v2 with itself = %+v, want the same model and no changes", c)
	}
	if c.Changes == nil {
		t.Fatal("changes = null, want an empty list a client can iterate")
	}
}

// TestProcessComparisonRefusals: a key that is not a number or names nothing, a
// version of another process, and a first version with nothing before it are each
// said as what they are.
func TestProcessComparisonRefusals(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployVersion(t, ts, incidentUserTaskBPMN)
	other := deployVersion(t, ts, strings.Replace(incidentUserTaskBPMN, `id="approval"`, `id="other"`, 1))

	for _, tc := range []struct {
		path string
		want int
		says string
	}{
		{"/api/v1/processes/abc/comparison", http.StatusBadRequest, "invalid definition key"},
		{fmt.Sprintf("/api/v1/processes/%d/comparison?with=abc", v1), http.StatusBadRequest, "?with="},
		{"/api/v1/processes/987654321/comparison", http.StatusNotFound, "no deployment"},
		{fmt.Sprintf("/api/v1/processes/%d/comparison?with=987654321", v1), http.StatusNotFound, "?with="},
		{fmt.Sprintf("/api/v1/processes/%d/comparison?with=%d", v1, other), http.StatusBadRequest, "only versions of one process"},
		{fmt.Sprintf("/api/v1/processes/%d/comparison", v1), http.StatusNotFound, "first version"},
	} {
		code, body := doReq(t, ts, http.MethodGet, tc.path, "", "")
		if code != tc.want || !strings.Contains(string(body), tc.says) {
			t.Errorf("GET %s = %d %s, want %d saying %q", tc.path, code, body, tc.want, tc.says)
		}
	}
}

// TestDraftComparisonListsEveryChange: the Modeler's chip read only the first
// difference between the draft and a version; the comparison now carries all of
// them, in the same form as a comparison of two versions.
func TestDraftComparisonListsEveryChange(t *testing.T) {
	ts := newTestServer(t)
	deployKey(t, ts, ts.Client(), deployableBPMN("order"))
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", renamedTask("order", "Ship it"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts/order/comparison", "", "")
	if code != http.StatusOK {
		t.Fatalf("comparison = %d %s", code, body)
	}
	var c struct {
		SameModel bool             `json:"sameModel"`
		Changes   []comparedChange `json:"changes"`
		Counts    *struct {
			Changed int `json:"changed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	want := `changed serviceTask t [added name: "" -> "Ship it"]`
	if c.SameModel || summarize(c.Changes) != want || c.Counts == nil || c.Counts.Changed != 1 {
		t.Fatalf("draft against v1:\n%s\nwant\n%s (%s)", summarize(c.Changes), want, body)
	}
}

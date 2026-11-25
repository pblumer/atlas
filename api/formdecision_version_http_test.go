package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestTheFormEditorIsToldWhichVersionAFieldEvaluates: the decisions a field's options
// can come from are listed with the deployment that answers now — the newest version,
// its key and the model it came from — so the form editor can say which version a
// field shows without the author opening the decision editor.
func TestTheFormEditorIsToldWhichVersionAFieldEvaluates(t *testing.T) {
	ts := newTestServer(t)
	for _, entries := range [][]string{{`"CC100"`}, {`"CC100"`, `"CC200"`}} {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments", stammdatenDMN(entries...), "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy decision: %d %s", code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/form-data/decisions", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list struct {
		Decisions []struct {
			ID         string `json:"id"`
			Deployment *struct {
				Key          uint64 `json:"key"`
				Version      int32  `json:"version"`
				ModelName    string `json:"modelName"`
				ResourceName string `json:"resourceName"`
				DeployedAt   int64  `json:"deployedAt"`
			} `json:"deployment"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	var found bool
	for _, d := range list.Decisions {
		if d.ID != "Kostenstellen" {
			continue
		}
		found = true
		dep := d.Deployment
		if dep == nil || dep.Version != 2 || dep.Key == 0 || dep.ModelName != "Stammdaten" || dep.DeployedAt == 0 {
			t.Fatalf("Kostenstellen's deployment = %+v; want version 2 of Stammdaten, with its key and time (%s)", dep, body)
		}
	}
	if !found {
		t.Fatalf("Kostenstellen not listed: %s", body)
	}
}

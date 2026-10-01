package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Saving a Playground scenario and its baseline when the body cannot be read or
// the record cannot be written. A scenario that answered 200 and was not kept
// would be found missing the next time somebody wanted to run it, and a baseline
// that silently did not save would have the next run measured against the old one.

// playgroundScenariosPathsSpec is the smallest spec the shape check accepts.
const playgroundScenariosPathsSpec = `{"open":{"source":"draft","ref":"p"},"run":{"cases":[{"n":1}]}}`

// playgroundScenariosPathsSave posts a scenario.
func playgroundScenariosPathsSave(t *testing.T, s *Server, id string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/playground/scenarios",
		strings.NewReader(`{"id":"`+id+`","processId":"p","spec":`+playgroundScenariosPathsSpec+`}`))
}

// playgroundScenariosPathsGet reads one scenario back as the API returns it.
func playgroundScenariosPathsGet(t *testing.T, s *Server, id string) (int, map[string]any) {
	t.Helper()
	code, body := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/playground/scenarios/"+id, nil)
	var out map[string]any
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return code, out
}

// TestPlaygroundScenariosAnUnreadableBodyIsRefused, for the scenario and for its
// baseline, and neither is changed.
func TestPlaygroundScenariosAnUnreadableBodyIsRefused(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, "/api/v1/playground/scenarios", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("save = %d %s, want 400", code, body)
	}

	if code, body := playgroundScenariosPathsSave(t, srv, "sc1"); code != http.StatusOK {
		t.Fatalf("save sc1 = %d %s", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPut, "/api/v1/playground/scenarios/sc1/baseline", errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("baseline = %d %s, want 400", code, body)
	}
	if _, got := playgroundScenariosPathsGet(t, srv, "sc1"); got["baseline"] != nil {
		t.Errorf("scenario = %+v, want no baseline", got)
	}
}

// TestPlaygroundScenariosAWriteThatFailsIsReported. The record cannot be
// written: the scenario is not saved, a baseline is not recorded, and both say so.
func TestPlaygroundScenariosAWriteThatFailsIsReported(t *testing.T) {
	t.Run("the scenario", func(t *testing.T) {
		srv := newServerForErrors(t)
		reconcileApplyPathsBlockSave(t, srv.playgroundScenarios.FileFor("sc2"))

		if code, body := playgroundScenariosPathsSave(t, srv, "sc2"); code != http.StatusInternalServerError ||
			!strings.Contains(body, "save scenario") {
			t.Errorf("save = %d %s, want 500", code, body)
		}
		if code, _ := playgroundScenariosPathsGet(t, srv, "sc2"); code != http.StatusNotFound {
			t.Errorf("get after a failed save = %d, want 404", code)
		}
	})

	t.Run("the baseline", func(t *testing.T) {
		srv := newServerForErrors(t)
		if code, body := playgroundScenariosPathsSave(t, srv, "sc3"); code != http.StatusOK {
			t.Fatalf("save = %d %s", code, body)
		}
		reconcileApplyPathsBlockSave(t, srv.playgroundScenarios.FileFor("sc3"))

		code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPut, "/api/v1/playground/scenarios/sc3/baseline",
			strings.NewReader(`{"cases":1}`))
		if code != http.StatusInternalServerError || !strings.Contains(body, "save baseline") {
			t.Errorf("baseline = %d %s, want 500", code, body)
		}
		if _, got := playgroundScenariosPathsGet(t, srv, "sc3"); got["baseline"] != nil {
			t.Errorf("scenario = %+v, want no baseline recorded", got)
		}
	})
}

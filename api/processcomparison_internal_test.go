package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestProcessComparisonOfAnUnreadableVersion drives the 500 a normal HTTP flow cannot
// reach: a deployed version whose stored document is not XML, which a deploy refuses,
// so only something writing the registry around it could leave one. The comparison
// names what it could not do rather than answering with an empty list, which would
// read as "nothing changed".
func TestProcessComparisonOfAnUnreadableVersion(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) (int, string) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	deploy := func(xml string) uint64 {
		code, body := do(http.MethodPost, "/api/v1/deployments", xml)
		if code != http.StatusOK {
			t.Fatalf("deploy = %d %s", code, body)
		}
		var d struct {
			Key uint64 `json:"key"`
		}
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatalf("decode deploy: %v (%s)", err, body)
		}
		return d.Key
	}
	deploy(comparedOrder)
	v2 := deploy(strings.Replace(comparedOrder, `retries="3"`, `retries="5"`, 1))
	srv.do(func() { srv.deployments[v2].xml = []byte("<definitions>") })

	code, body := do(http.MethodGet, "/api/v1/processes/"+strconv.FormatUint(v2, 10)+"/comparison", "")
	if code != http.StatusInternalServerError || !strings.Contains(body, "compare the versions") {
		t.Fatalf("comparison with an unreadable version = %d %s, want a 500 saying so", code, body)
	}
}

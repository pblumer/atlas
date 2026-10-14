package capability

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// decodeJSONLimit reads a bounded JSON body. The ceiling comes from the
// installation's budgets rather than from a constant here, so the set of ceilings
// stays enumerable and an operator can move them (ADR-0291).
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if int64(len(body)) > limit {
		httpapi.Error(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

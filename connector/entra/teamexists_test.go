package entra

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestTeamExistsReadsGraphsConflict: a create-team that Graph refuses with 409 found
// the group a team already; any other refusal, or an error that is not Graph's, is not
// that.
func TestTeamExistsReadsGraphsConflict(t *testing.T) {
	conflict := &GraphError{Method: "PUT", Path: "/groups/g/team", Status: http.StatusConflict}
	if !teamExists(conflict) || !teamExists(fmt.Errorf("create-team: %w", conflict)) {
		t.Error("a 409 from Graph is not read as an existing team")
	}
	if teamExists(&GraphError{Method: "PUT", Path: "/groups/g/team", Status: http.StatusBadRequest}) {
		t.Error("a 400 from Graph is read as an existing team")
	}
	if teamExists(errors.New("connection reset")) {
		t.Error("an error that is not Graph's is read as an existing team")
	}
}

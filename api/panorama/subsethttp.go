package panorama

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
)

// HandleSubset serves the authoring subset: what may be created, what may be drawn
// between what, and the statement that it is a subset (ADR-0189 §2, P2b).
//
// It is served rather than duplicated in the browser, and that is the whole reason
// this route exists. The canvas has to refuse a connection while it is being
// dragged; the server has to refuse it on write. Two copies of a relationship
// matrix is how you get a canvas that lets somebody draw an arrow the server then
// rejects — so there is one table, and the browser is given it.
//
// It reads no model and takes no id: the subset is a property of this build, not of
// anybody's document, and asking for it discloses nothing about what exists.
func (s *Service) HandleSubset(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, AuthoringSubset())
}

package api

import (
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// A catalogue maintained in another system: the server's half
// (ADR-0565).
//
// The catalogue service decides every write to such a catalogue (api/catalog,
// maintained.go). What lives here is what only the server knows: which grant a request
// comes through, the two writes the server makes to a catalogue itself — deleting one
// or a product for good, whose check is heldPurge in catalogpurge.go — and the audit
// trail an administrator's take-back is recorded in. This file names no type of the
// shop's packages (ADR-0515).

// catalogPublisher answers the system a request comes through when it is a person's
// grant for that system's shop, the one way the import marks a catalogue as maintained
// elsewhere. It is the server's to answer because the scope is: the catalogue package
// reads no principal's scope (ADR-0493).
func catalogPublisher(p *httpapi.Principal) (client, name string) {
	if p == nil || p.Scope != apiScopeShop || p.ClientID == "" {
		return "", ""
	}
	return p.ClientID, p.ClientName
}

// handleTakeBackCatalog serves DELETE /api/v1/catalogs/{id}/maintainer: an
// administrator takes a catalogue back from the system it is maintained in, so it is
// maintained here again. Recorded in the audit trail with the system it was taken
// from, because "why does the wiki's publish no longer win" is the question it answers.
func (s *Server) handleTakeBackCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cat, was, code, msg := s.catalogs.TakeBack(httpapi.PrincipalFrom(r.Context()), id)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if was != nil {
		audit(r, logging.CatalogMaintainerCleared,
			"catalogue: taken back from the system it was maintained in",
			slog.String("catalog", id), slog.String("client_id", was.Client),
			slog.String("client_name", was.Name), slog.String("url", was.URL))
	}
	httpapi.JSON(w, http.StatusOK, cat)
}

package catalog

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue maintained in another system
// (ADR-draft-a-shop-and-its-catalogues-from-another-system).
//
// A wiki that keeps a catalogue as its own pages publishes it into Atlas through the
// document import (ADR-0436). The import replaces records with the same id, so a
// change made here in between — a product renamed in the Console, an edge added —
// would be overwritten by the next publish without anybody being told. So a
// catalogue the import wrote through a system's grant carries that system
// ([Catalog.MaintainedIn]), and every other write to it, or to a product whose home
// it is, is refused with the system's name and address: the change belongs there.
//
// The decision is made in one place, heldElsewhere, and every write path asks it
// through mayWrite right after mayEdit, inside the same run-loop turn as the write it
// guards. The writes that stay open are an administrator's — a catalogue's theme and
// logo (ADR-0316) and pruning its releases — and the ones that read and follow
// rather than author: a content refresh fetches what the binding the document set
// names, and a rebind moves orders, which Atlas keeps. The api package's
// TestEveryCatalogueWriteIsHeldOrOpenOnPurpose holds every write route of the
// catalogue to one of the two lists.

// heldElsewhere answers the system a catalogue is maintained in when p may not write
// it here, and nil when p may: the catalogue is maintained here, or the request comes
// through that system's own grant for the shop. It reads nothing.
func (s *Service) heldElsewhere(c Catalog, p *httpapi.Principal) *Maintainer {
	m := c.MaintainedIn
	if m == nil {
		return nil
	}
	if client, _ := s.publisher(p); client != "" && client == m.Client {
		return nil
	}
	return m
}

// HeldElsewhere is heldElsewhere for the server's own writes to a catalogue — deleting
// one or a product for good — which live outside this package.
func (s *Service) HeldElsewhere(c Catalog, p *httpapi.Principal) *Maintainer {
	return s.heldElsewhere(c, p)
}

// mayWrite is mayEdit for a write: allowed is mayEdit's answer, and held, asked only
// of a caller who may edit, is the system the catalogue is maintained in when the
// write must be made there instead. Authority is answered first, so a caller who may
// not change the catalogue learns nothing about where it is maintained.
func (s *Service) mayWrite(c Catalog, p *httpapi.Principal) (allowed bool, held *Maintainer) {
	if !s.mayEdit(c, p) {
		return false, nil
	}
	return true, s.heldElsewhere(c, p)
}

// publisher answers the system a request comes through when it is a person's grant
// for the shop of that system — the one way a document's catalogues are marked as
// maintained elsewhere — and empty strings for every other request.
func (s *Service) publisher(p *httpapi.Principal) (client, name string) {
	if s.Publisher == nil || p == nil {
		return "", ""
	}
	return s.Publisher(p)
}

// HeldMessage is the sentence a write refused for a catalogue maintained elsewhere is
// answered with: what is maintained where, where to go instead, and how an
// administrator takes the catalogue back. subject is what was refused ("catalogue
// x", "product y"), catalogID the catalogue that holds it.
func HeldMessage(subject, catalogID string, m *Maintainer) string {
	where := m.Name
	if where == "" {
		where = m.Client
	}
	msg := subject + " is maintained in " + where
	if m.URL != "" {
		msg += " (" + m.URL + ")"
	}
	return msg + "; change it there. A change made here would be overwritten by the next publish from " +
		where + ". An administrator can take catalogue " + catalogID +
		" back with DELETE /api/v1/catalogs/" + catalogID + "/maintainer"
}

// RefuseHeld answers a write refused because the catalogue is maintained elsewhere:
// 409, the sentence, and the system as data, so a client can link to it.
func RefuseHeld(w http.ResponseWriter, subject, catalogID string, m *Maintainer) {
	httpapi.JSON(w, http.StatusConflict, map[string]any{
		"error":        HeldMessage(subject, catalogID, m),
		"maintainedIn": m,
	})
}

// problemMaintainerURL is the problem an import through a system's grant is refused
// with when its document names a place of maintenance that is not an https address.
const problemMaintainerURL = "maintainedIn.url must be an https address, or left out"

// maintainerURL reports whether u may stand as where a catalogue is maintained: empty,
// or an absolute https address with a host. Anything else would be a link the Console
// draws that leads somewhere nobody vouched for.
func maintainerURL(u string) bool {
	if u == "" {
		return true
	}
	parsed, err := url.Parse(u)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		!strings.ContainsAny(u, " \t\r\n")
}

// TakeBack clears where a catalogue is maintained, so it is maintained here again:
// DELETE /api/v1/catalogs/{id}/maintainer, an administrator's (the route says so). It
// answers the catalogue as it is now and the system it was maintained in — nil when
// it was maintained here already, which is not an error: the catalogue is where the
// caller wants it — with code and msg the refusal, code 0 when it answers.
func (s *Service) TakeBack(p *httpapi.Principal, id string) (Catalog, *Maintainer, int, string) {
	var (
		got            Catalog
		was            *Maintainer
		found, allowed bool
		opErr          error
	)
	s.loop.Do(func() {
		if got, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(got, p); !found {
			return
		}
		if allowed = s.admin(p); !allowed {
			return
		}
		if was = got.MaintainedIn; was == nil {
			return
		}
		got.MaintainedIn = nil
		got.Revision++
		got.UpdatedAt = s.now()
		opErr = s.store.SaveCatalog(got)
	})
	switch {
	case opErr != nil:
		return got, nil, http.StatusInternalServerError, "take the catalogue back: " + opErr.Error()
	case !found:
		return got, nil, http.StatusNotFound, "no catalogue " + id
	case !allowed:
		return got, nil, http.StatusForbidden, "only an administrator takes a catalogue back from the system it is maintained in"
	}
	return got, was, 0, ""
}

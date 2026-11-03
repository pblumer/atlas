package order

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
)

// A process orders in the name of its application (ADR-0520).
//
// An HR joiner process orders the starter package for a person who joins. It has no
// caller in the sense HandlePlace asks about — no session, no groups, no catalogue
// audience — so it is admitted the way a shop command task is (ADR-0429 §10): every
// product it asks for must name the process's application in its OrderableBy, read from
// the newest release of the catalogue the task names. Past that door the order is
// placed by the same code as a person's (place), so it meets every rule the release
// states — eligibility, conflicts, the orderable window, answers and variants — and the
// product's own approval rule.

// ApplicationPrincipalPrefix is what an order placed by a process names as its orderer,
// before the application's key. It is a principal nobody signs in as, which is the
// point: the order is the application's act, and a report or an audit names it so.
const ApplicationPrincipalPrefix = "system:application:"

// ApplicationPrincipal is the orderer an application's process orders as.
func ApplicationPrincipal(app string) string { return ApplicationPrincipalPrefix + app }

// ApplicationOrderID is the order id a process's order is placed under: derived from
// the task's command id, which survives the job's retries, so a retried task finds the
// order it already placed instead of placing a second one.
func ApplicationOrderID(commandID string) string {
	sum := sha256.Sum256([]byte("application-order:" + commandID))
	return "ord_" + hex.EncodeToString(sum[:12])
}

// ApplicationOrder is what a process orders.
type ApplicationOrder struct {
	// App is the portable key of the process's application.
	App string
	// CommandID identifies the asking task across its retries.
	CommandID string
	// Release is the newest release of the catalogue the task names.
	Release catalog.Release
	// Items, Recipient, Config and Variants are what a basket would send.
	Items     []string
	Recipient string
	Config    map[string]map[string]string
	Variants  map[string][]string
}

// PlaceForApplication places an order for a process. It answers the order and whether
// it was placed now (false when a retry found the one an earlier attempt placed), or a
// *Refusal saying why not — the same sentence the shop would show for the same basket.
//
// A retry that finds its order does not start the fulfilment again: an order that was
// stored but whose fulfilment could not be started is what the fulfilment repair is for
// (api/fulfilmentrepair.go), and starting it twice would work the order twice.
func (s *Service) PlaceForApplication(a ApplicationOrder) (Order, bool, error) {
	app := strings.TrimSpace(a.App)
	if app == "" {
		return Order{}, false, refuse(http.StatusForbidden,
			"the process belongs to no application; only an application a product lists may order it")
	}
	if strings.TrimSpace(a.CommandID) == "" {
		return Order{}, false, fmt.Errorf("order: a process's order needs the id of the task asking")
	}
	recipient := strings.TrimSpace(a.Recipient)
	if recipient == "" {
		return Order{}, false, refuse(http.StatusBadRequest,
			"the task resolved no recipient; a process orders for somebody, by their account id")
	}
	if len(a.Items) == 0 {
		return Order{}, false, refuse(http.StatusBadRequest, "the task orders no product")
	}
	for _, id := range a.Items {
		it, ok := releaseItem(a.Release, id)
		switch {
		case !ok:
			return Order{}, false, refuse(http.StatusNotFound,
				fmt.Sprintf("the newest release of catalogue %q does not offer %q", a.Release.CatalogID, id))
		case !slices.Contains(it.OrderableBy, app):
			return Order{}, false, refuse(http.StatusForbidden,
				fmt.Sprintf("%q does not list application %q among those that may order it", id, app))
		}
	}
	id := ApplicationOrderID(a.CommandID)
	var (
		existing Order
		found    bool
		getErr   error
	)
	s.loop.Do(func() { existing, found, getErr = s.store.Get(id) })
	switch {
	case getErr != nil:
		return Order{}, false, getErr
	case found:
		return existing, false, nil
	}
	out, refusal := s.place(ApplicationPrincipal(app), recipient, id, a.Release, placeReq{
		ReleaseID: a.Release.ID, Items: a.Items, Recipient: recipient,
		Config: a.Config, Variants: a.Variants,
	})
	if refusal != nil {
		return out, out.ID != "", refusal
	}
	return out, true, nil
}

func releaseItem(rel catalog.Release, id string) (catalog.Item, bool) {
	for _, it := range rel.Items {
		if it.ID == id {
			return it, true
		}
	}
	return catalog.Item{}, false
}

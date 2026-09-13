package api

import (
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Reading the inventory.
//
// "What does this person hold today" is the question the whole third model exists
// to answer (ADR-0312), and it is not the same
// question as "what did they order". Orders end; rights do not, and the order
// that granted one is eligible for retention deletion long before the right is.
// So this reads the entitlement family and never the order store — an answer
// assembled from orders would start losing rights on the ninetieth day.

// heldItem is one thing somebody holds.
//
// It carries ids and moments and nothing about the person. Names, addresses and
// departments resolve from the account when a screen is rendered
// (ADR-0314); the inventory stores a principal reference,
// so there is nothing else here to return.
type heldItem struct {
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`
	// Since is when the hold began, as the grant recorded it.
	Since int64 `json:"since"`
	// Origin says where the knowledge came from: ordered, adopted or legacy. It is
	// written out as a word rather than as its byte, because a reader deciding
	// whether to trust a row should not have to look up an enum.
	Origin string `json:"origin"`
	// OrderID is empty for anything Atlas did not grant itself, which is the honest
	// answer rather than an omission.
	OrderID string `json:"orderId,omitempty"`
}

// inventoryResp is one principal's inventory. The principal is echoed because the
// answer can be about somebody other than the caller, and a list with no subject
// is a list somebody will attribute to the wrong person.
type inventoryResp struct {
	Principal string     `json:"principal"`
	Items     []heldItem `json:"items"`
}

// handleInventory answers what one principal holds.
//
// By default that is the caller. An administrator may ask about somebody else
// with ?principal=, which is what a leaver process and an audit need; nobody else
// may, because an inventory is a list of somebody's access and reading it is the
// same act whether it is used well or badly.
//
// The scan runs off the loop (ADR-0239). It is bounded by one person's inventory
// rather than by the population, but it is still a scan, and the single writer
// does not do scans.
func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	pr := httpapi.PrincipalFrom(r.Context())

	principal := ""
	if pr != nil {
		principal = pr.UserID
	}
	if asked := r.URL.Query().Get("principal"); asked != "" && asked != principal {
		if !s.isAdmin(r) {
			// Named rather than silently narrowed to the caller's own: an answer
			// that quietly changes the question is how somebody comes to believe
			// they read a colleague's access and found it empty.
			httpapi.Error(w, http.StatusForbidden,
				"reading somebody else's inventory needs the admin role")
			return
		}
		principal = asked
	}
	if principal == "" {
		// With auth off there is no caller to be, so there is no default subject
		// either. Asking for one by name still works.
		httpapi.Error(w, http.StatusBadRequest,
			"no principal: name one with ?principal= when nobody is signed in")
		return
	}

	items := []heldItem{}
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.EntitlementsOf(principal, func(v *model.EntitlementValue) error {
			items = append(items, heldItem{
				ItemID: v.ItemID, VariantID: v.VariantID, Since: v.Since,
				Origin: v.Origin.String(), OrderID: v.OrderID,
			})
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read inventory: "+err.Error())
		return
	}
	// Newest first: somebody opening this page is asking about what changed, and
	// the scan's own order is by item id, which is an implementation detail of the
	// key and not an order anybody asked for.
	sort.SliceStable(items, func(i, j int) bool { return items[i].Since > items[j].Since })

	httpapi.JSON(w, http.StatusOK, inventoryResp{Principal: principal, Items: items})
}

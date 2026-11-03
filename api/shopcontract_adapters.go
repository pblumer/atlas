package api

import (
	"log/slog"
	"net/http"

	"github.com/pblumer/atlas/api/shopcontract"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The engine's side of the shop contract (api/shopcontract; ADR-0515). The shop's
// packages reach the inventory, the directory and the audit through these, and
// through nothing else of the server.

// shopInventory reads the inventory off the run loop.
type shopInventory struct{ s *Server }

func (i shopInventory) Read(fn func(shopcontract.InventoryView) error) error {
	return i.s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return fn(inventoryView{rv})
	})
}

type inventoryView struct{ rv *state.ReadView }

func (v inventoryView) EachHold(fn func(shopcontract.Hold) error) error {
	return v.rv.Entitlements(func(e *model.EntitlementValue) error {
		return fn(shopcontract.Hold{Principal: e.Principal, ItemID: e.ItemID, VariantID: e.VariantID,
			OrderID: e.OrderID, Origin: e.Origin.String(), Since: e.Since, Until: e.Until})
	})
}

func (v inventoryView) HistoryOf(principal string, fn func(shopcontract.Hold) error) error {
	return v.rv.EntitlementHistoryOf(principal, func(e *model.EntitlementHistoryValue) error {
		return fn(shopcontract.Hold{Principal: e.Principal, ItemID: e.ItemID, VariantID: e.VariantID,
			OrderID: e.OrderID, Origin: e.Origin.String(), Since: e.Since, Until: e.Until})
	})
}

// shopDirectory lists the accounts with their groups, read off the loop: both
// stores are sidecars (ADR-0265).
type shopDirectory struct{ s *Server }

func (d shopDirectory) Accounts() ([]shopcontract.Account, error) {
	if d.s.users == nil {
		return nil, nil
	}
	users, err := d.s.users.LoadAll()
	if err != nil {
		return nil, err
	}
	memberOf := map[string][]string{}
	if d.s.groups != nil {
		groups, err := d.s.groups.LoadAll()
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			for _, m := range g.Members {
				memberOf[m] = append(memberOf[m], g.ID)
			}
		}
	}
	out := make([]shopcontract.Account, 0, len(users))
	for _, u := range users {
		out = append(out, shopcontract.Account{ID: u.ID, Name: firstNonEmpty(u.DisplayName, firstNonEmpty(u.Username, u.ID)),
			ManagerID: u.ManagerID, Disabled: u.Disabled, Groups: memberOf[u.ID]})
	}
	return out, nil
}

// auditNamesRead writes the line a report that names people owes (ADR-0515 §3).
func auditNamesRead(r *http.Request, attrs ...slog.Attr) {
	audit(r, logging.ReportNamesRead, "read a report that names people", attrs...)
}

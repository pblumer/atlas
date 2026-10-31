package api

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/servicereport"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The product manager's delivery report
// (ADR-0515 §3).
//
// How the products a product manager maintains were ordered, delivered against
// their promise and are held now. Counts and durations only: ADR-0353 decided that
// the person maintaining a service does not need to know who holds it, and that
// stays true when the question is how fast it arrived.
//
// Everything is read off the run loop: the catalogue and order stores are read
// directly, which a sidecar store permits (ADR-0265), and the inventory through a
// read view (ADR-0239). Nothing is stored; every call computes the report anew.

// deliveryWindowDays is the period a report covers when the caller names none: a
// quarter, the period a service review usually looks back over.
const deliveryWindowDays = 90

type deliveryReport struct {
	From        string                     `json:"from"`
	To          string                     `json:"to"`
	Catalogs    []string                   `json:"catalogs"`
	Products    []servicereport.ProductRow `json:"products"`
	Totals      servicereport.Totals       `json:"totals"`
	GeneratedAt int64                      `json:"generatedAt"`
	Note        string                     `json:"note,omitempty"`
}

// reportWindow reads ?from= and ?to= as dates in the server's zone and answers the
// half-open interval [from 00:00, to+1 00:00) in unix nanoseconds, with the dates as
// written. A missing bound defaults to a window of deliveryWindowDays ending today.
func reportWindow(r *http.Request, now time.Time, loc *time.Location) (fromNS, toNS int64, from, to string, msg string) {
	q := r.URL.Query()
	parse := func(name, v string) (time.Time, bool) {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			msg = "?" + name + "= is a date written YYYY-MM-DD, not " + v
			return time.Time{}, false
		}
		return d, true
	}
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	end, start := today, today.AddDate(0, 0, -deliveryWindowDays+1)
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		d, ok := parse("to", v)
		if !ok {
			return 0, 0, "", "", msg
		}
		end = d
		if q.Get("from") == "" {
			start = end.AddDate(0, 0, -deliveryWindowDays+1)
		}
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		d, ok := parse("from", v)
		if !ok {
			return 0, 0, "", "", msg
		}
		start = d
	}
	if end.Before(start) {
		return 0, 0, "", "", "the period ends before it begins: ?to= is earlier than ?from="
	}
	return start.UnixNano(), end.AddDate(0, 0, 1).UnixNano(),
		start.Format("2006-01-02"), end.Format("2006-01-02"), ""
}

// itemNameIn answers a product's name for a reader of lang: that language's, else
// the first language the catalogue declares that has one, else the id.
func itemNameIn(it catalog.Item, lang string, langs []string) string {
	if t := strings.TrimSpace(it.Texts[lang]); t != "" {
		return t
	}
	for _, l := range langs {
		if t := strings.TrimSpace(it.Texts[l]); t != "" {
			return t
		}
	}
	keys := make([]string, 0, len(it.Texts))
	for k := range it.Texts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if t := strings.TrimSpace(it.Texts[k]); t != "" {
			return t
		}
	}
	return it.ID
}

// reportScope is what a report reads: which releases belong to the catalogues in
// scope, the products those catalogues offer now and what they are called.
type reportScope struct {
	catalogs []string
	releases map[string]bool
	items    map[string]bool
	names    map[string]string
}

// scopeOf loads the releases of the given catalogues. Read directly from the
// sidecar store, off the loop.
func (s *Server) scopeOf(cats []catalog.Catalog, lang string) (reportScope, error) {
	sc := reportScope{releases: map[string]bool{}, items: map[string]bool{}, names: map[string]string{}}
	for _, c := range cats {
		rels, err := s.catalogStore.ReleasesOf(c.ID)
		if err != nil {
			return sc, err
		}
		sc.catalogs = append(sc.catalogs, c.ID)
		for i, rel := range rels {
			sc.releases[rel.ID] = true
			for _, it := range rel.Items {
				// The newest release names a product; an older one only where the
				// product is no longer offered, so a retired product keeps a name.
				if _, named := sc.names[it.ID]; i == 0 || !named {
					sc.names[it.ID] = itemNameIn(it, lang, c.Languages)
				}
				if i == 0 {
					sc.items[it.ID] = true
				}
			}
		}
	}
	sort.Strings(sc.catalogs)
	return sc, nil
}

// scopeRead is what one read of a report's scope found.
type scopeRead struct {
	positions []servicereport.Position
	// heldCount is the current rights per product in scope.
	heldCount map[string]int
	// held is every current right of the people asked for; nil without people.
	held []servicereport.Held
	// orders is every order by id, for the price a held right was ordered at.
	orders map[string]order.Order
}

// measureScope reads the orders placed against the scope's releases in the window
// and the holds that date them, and measures every position. It also counts the
// current rights to the scope's products. people, when not nil, narrows both to
// the orders and holds of those principals, and then also answers every right they
// hold to a product the scope ever offered.
func (s *Server) measureScope(sc reportScope, fromNS, toNS int64, people map[string]bool) (scopeRead, error) {
	all, err := s.orderStore.All()
	if err != nil {
		return scopeRead{}, err
	}
	out := scopeRead{heldCount: map[string]int{}, orders: make(map[string]order.Order, len(all))}
	var orders []order.Order
	ids := map[string]bool{}
	returned := map[string]bool{}
	for _, o := range all {
		out.orders[o.ID] = o
		if !sc.releases[o.ReleaseID] || o.CreatedAt < fromNS || o.CreatedAt >= toNS {
			continue
		}
		if people != nil && !people[o.Recipient] {
			continue
		}
		orders = append(orders, o)
		ids[o.ID] = true
		for _, l := range o.Lines {
			if l.Status == order.StatusReturned {
				returned[o.Recipient] = true
			}
		}
	}

	var holds []servicereport.Hold
	err = s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		if err := rv.Entitlements(func(v *model.EntitlementValue) error {
			if people != nil {
				if !people[v.Principal] {
					return nil
				}
				if _, offered := sc.names[v.ItemID]; offered {
					out.held = append(out.held, servicereport.Held{Principal: v.Principal, ItemID: v.ItemID,
						VariantID: v.VariantID, OrderID: v.OrderID, Origin: v.Origin.String(),
						Since: v.Since, Until: v.Until})
				}
			}
			if !sc.items[v.ItemID] {
				return nil
			}
			out.heldCount[v.ItemID]++
			if ids[v.OrderID] {
				holds = append(holds, servicereport.Hold{Principal: v.Principal, ItemID: v.ItemID,
					VariantID: v.VariantID, OrderID: v.OrderID, Since: v.Since})
			}
			return nil
		}); err != nil {
			return err
		}
		// A position given back is dated by the history of its hold. Read per person
		// who gave one back, not for everybody: the history is keyed by principal.
		for principal := range returned {
			if err := rv.EntitlementHistoryOf(principal, func(v *model.EntitlementHistoryValue) error {
				if ids[v.OrderID] {
					holds = append(holds, servicereport.Hold{Principal: v.Principal, ItemID: v.ItemID,
						VariantID: v.VariantID, OrderID: v.OrderID, Since: v.Since})
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return scopeRead{}, err
	}
	now := time.Unix(0, s.now())
	out.positions = servicereport.Measure(orders, holds, now, time.Local)
	return out, nil
}

// handleDeliveryReport answers the delivery report over the catalogues the caller
// maintains, or the one ?catalog= names. ?format=csv answers it as CSV.
func (s *Server) handleDeliveryReport(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	now := time.Unix(0, s.now())
	fromNS, toNS, from, to, msg := reportWindow(r, now, time.Local)
	if msg != "" {
		httpapi.Error(w, http.StatusBadRequest, "delivery report: "+msg)
		return
	}
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
		return
	}
	only := strings.TrimSpace(r.URL.Query().Get("catalog"))
	var mine []catalog.Catalog
	for _, c := range cats {
		if (only == "" || c.ID == only) && s.catalogs.MayEdit(c, p) {
			mine = append(mine, c)
		}
	}
	if only != "" && len(mine) == 0 {
		// Not found and not yours read the same: whether a catalogue exists is not
		// something this report confirms to somebody who does not maintain it.
		httpapi.Error(w, http.StatusNotFound, "delivery report: you maintain no catalogue "+only)
		return
	}
	sc, err := s.scopeOf(mine, reportLang(r))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
		return
	}
	read, err := s.measureScope(sc, fromNS, toNS, nil)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
		return
	}
	rows := servicereport.Summarise(read.positions, sc.names, read.heldCount)
	if r.URL.Query().Get("format") == "csv" {
		var b bytes.Buffer
		if err := servicereport.WriteProductsCSV(&b, rows); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
			return
		}
		writeCSV(w, "lieferung-"+from+"-"+to+".csv", b.Bytes())
		return
	}
	rep := deliveryReport{From: from, To: to, Catalogs: sc.catalogs, Products: rows,
		Totals: servicereport.Total(rows), GeneratedAt: now.UnixNano()}
	if rep.Catalogs == nil {
		rep.Catalogs = []string{}
		rep.Note = "you maintain no catalogue, so there is nothing to report"
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

// writeCSV answers a CSV download with a byte-order mark, so a spreadsheet reads
// the umlauts as UTF-8 rather than guessing a legacy code page.
func writeCSV(w http.ResponseWriter, filename string, body []byte) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("\ufeff"))
	_, _ = w.Write(body)
}

// reportLang is the language a report names products in: ?lang=, else German, the
// language the Console speaks first (ADR-0267).
func reportLang(r *http.Request) string {
	if l := strings.TrimSpace(r.URL.Query().Get("lang")); l != "" {
		return l
	}
	return "de"
}

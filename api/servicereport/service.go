package servicereport

import (
	"bytes"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/shopcontract"
)

// Service answers the four report routes: the product manager's delivery report
// and the reports that name people (ADR-0515). It is a service of its own in the
// sense of ADR-0147 and, more to the point of tshopper's phase A, it reaches the
// engine only through the contract in api/shopcontract: the inventory as a read
// view, the directory as a list of accounts, and the audit as one call. Everything
// else it reads — catalogues, releases, orders — is the shop's own.
//
// Every dependency is a field set once by the server, so the handlers carry no
// knowledge of where the stores live.
type Service struct {
	// Now answers the server's clock in unix nanoseconds.
	Now func() int64
	// Location is the zone dates and working days are read in; nil is the
	// process's local zone.
	Location *time.Location
	// Catalogs and ReleasesOf read the catalogue store; both are sidecar reads a
	// caller may make off the run loop (ADR-0265).
	Catalogs   func() ([]catalog.Catalog, error)
	ReleasesOf func(catalogID string) ([]catalog.Release, error)
	// MayEdit reports whether a principal maintains a catalogue.
	MayEdit func(catalog.Catalog, *httpapi.Principal) bool
	// Orders reads every order, off the run loop.
	Orders func() ([]order.Order, error)
	// Inventory and Directory are the engine's side of the contract.
	Inventory shopcontract.Inventory
	Directory shopcontract.Directory
	// NameOf answers a resolver from a principal id to the name a person reads.
	NameOf func() func(string) string
	// Audit writes the audit line an answer that names people owes, before it is
	// sent.
	Audit func(r *http.Request, attrs ...slog.Attr)
}

func (s *Service) loc() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

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
	From        string       `json:"from"`
	To          string       `json:"to"`
	Catalogs    []string     `json:"catalogs"`
	Products    []ProductRow `json:"products"`
	Totals      Totals       `json:"totals"`
	GeneratedAt int64        `json:"generatedAt"`
	Note        string       `json:"note,omitempty"`
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
func (s *Service) scopeOf(cats []catalog.Catalog, lang string) (reportScope, error) {
	sc := reportScope{releases: map[string]bool{}, items: map[string]bool{}, names: map[string]string{}}
	for _, c := range cats {
		rels, err := s.ReleasesOf(c.ID)
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
	positions []Position
	// heldCount is the current rights per product in scope.
	heldCount map[string]int
	// held is every current right of the people asked for; nil without people.
	held []Held
	// orders is every order by id, for the price a held right was ordered at.
	orders map[string]order.Order
}

// measureScope reads the orders placed against the scope's releases in the window
// and the holds that date them, and measures every position. It also counts the
// current rights to the scope's products. people, when not nil, narrows both to
// the orders and holds of those principals, and then also answers every right they
// hold to a product the scope ever offered.
func (s *Service) measureScope(sc reportScope, fromNS, toNS int64, people map[string]bool) (scopeRead, error) {
	all, err := s.Orders()
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

	var holds []Hold
	err = s.Inventory.Read(func(v shopcontract.InventoryView) error {
		if err := v.EachHold(func(h shopcontract.Hold) error {
			if people != nil {
				if !people[h.Principal] {
					return nil
				}
				if _, offered := sc.names[h.ItemID]; offered {
					out.held = append(out.held, Held{Principal: h.Principal, ItemID: h.ItemID,
						VariantID: h.VariantID, OrderID: h.OrderID, Origin: h.Origin,
						Since: h.Since, Until: h.Until})
				}
			}
			if !sc.items[h.ItemID] {
				return nil
			}
			out.heldCount[h.ItemID]++
			if ids[h.OrderID] {
				holds = append(holds, Hold{Principal: h.Principal, ItemID: h.ItemID,
					VariantID: h.VariantID, OrderID: h.OrderID, Since: h.Since})
			}
			return nil
		}); err != nil {
			return err
		}
		// A position given back is dated by the history of its hold. Read per person
		// who gave one back, not for everybody: the history is keyed by principal.
		for principal := range returned {
			if err := v.HistoryOf(principal, func(h shopcontract.Hold) error {
				if ids[h.OrderID] {
					holds = append(holds, Hold{Principal: h.Principal, ItemID: h.ItemID,
						VariantID: h.VariantID, OrderID: h.OrderID, Since: h.Since})
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
	now := time.Unix(0, s.Now())
	out.positions = Measure(orders, holds, now, s.loc())
	return out, nil
}

// HandleDeliveryReport answers the delivery report over the catalogues the caller
// maintains, or the one ?catalog= names. ?format=csv answers it as CSV.
func (s *Service) HandleDeliveryReport(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	now := time.Unix(0, s.Now())
	fromNS, toNS, from, to, msg := reportWindow(r, now, s.loc())
	if msg != "" {
		httpapi.Error(w, http.StatusBadRequest, "delivery report: "+msg)
		return
	}
	cats, err := s.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
		return
	}
	only := strings.TrimSpace(r.URL.Query().Get("catalog"))
	var mine []catalog.Catalog
	for _, c := range cats {
		if (only == "" || c.ID == only) && s.MayEdit(c, p) {
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
	rows := Summarise(read.positions, sc.names, read.heldCount)
	if r.URL.Query().Get("format") == "csv" {
		var b bytes.Buffer
		if err := WriteProductsCSV(&b, rows); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "delivery report: "+err.Error())
			return
		}
		writeCSV(w, "lieferung-"+from+"-"+to+".csv", b.Bytes())
		return
	}
	rep := deliveryReport{From: from, To: to, Catalogs: sc.catalogs, Products: rows,
		Totals: Total(rows), GeneratedAt: now.UnixNano()}
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

// The two reports that show names
// (ADR-0515 §3).
//
// The product manager's report counts; these two name. A line manager reads what
// their direct reports hold and have ordered; a customer responsible — a member of
// a group the catalogue names in reportingGroups — reads the same for everybody to
// whom that catalogue is the one they see (ADR-0347). Both relationships are stated
// somewhere a person decided them: the account's managerId (ADR-0470) and the
// catalogue's reporting groups. Nothing here infers one.
//
// Every answer that carries a name writes an audit line first. The answer is not
// sent without it: a report of who holds which right is the inventory, and reading
// it is an access worth being able to account for.

type peopleReport struct {
	// Scope is "team" or "organisation".
	Scope       string          `json:"scope"`
	Catalog     string          `json:"catalog,omitempty"`
	CatalogName string          `json:"catalogName,omitempty"`
	From        string          `json:"from"`
	To          string          `json:"to"`
	People      []PersonRow     `json:"people"`
	Positions   []namedPosition `json:"positions"`
	Products    []ProductRow    `json:"products"`
	Totals      Totals          `json:"totals"`
	GeneratedAt int64           `json:"generatedAt"`
}

// namedPosition is a position with the people it concerns by name and the product
// by its name.
type namedPosition struct {
	Position
	RecipientName string `json:"recipientName"`
	OrdererName   string `json:"ordererName,omitempty"`
	Name          string `json:"name"`
}

type myReports struct {
	// Team is how many people name the caller as their line manager.
	Team int `json:"team"`
	// Organisations are the catalogues whose reporting groups the caller is in.
	Organisations []myOrganisation `json:"organisations"`
}

type myOrganisation struct {
	Catalog string `json:"catalog"`
	Name    string `json:"name"`
}

// directReports answers the principal ids whose account names manager as theirs.
// Read off the loop: the user store is a sidecar.
func (s *Service) directReports(manager string) ([]string, error) {
	if manager == "" || s.Directory == nil {
		return nil, nil
	}
	all, err := s.Directory.Accounts()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range all {
		if u.ManagerID == manager && !u.Disabled {
			out = append(out, u.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// audienceOf answers the principals to whom cat is the catalogue they see: the
// people its groups reach, minus those a higher-ranked catalogue reaches first
// (catalog.Resolve).
func (s *Service) audienceOf(cat catalog.Catalog, cats []catalog.Catalog) ([]string, error) {
	if s.Directory == nil {
		return nil, nil
	}
	all, err := s.Directory.Accounts()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range all {
		if u.Disabled {
			continue
		}
		if got, ok := catalog.Resolve(cats, u.Groups); ok && got.ID == cat.ID {
			out = append(out, u.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// HandleMyReports answers which of the two reports the caller may open, so the
// shop draws a tab for each and none for the rest.
func (s *Service) HandleMyReports(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	out := myReports{Organisations: []myOrganisation{}}
	if p == nil {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	team, err := s.directReports(p.UserID)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "reports: "+err.Error())
		return
	}
	out.Team = len(team)
	cats, err := s.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "reports: "+err.Error())
		return
	}
	lang := reportLang(r)
	for _, c := range cats {
		if c.ReportsTo(p.GroupIDs) {
			out.Organisations = append(out.Organisations, myOrganisation{Catalog: c.ID,
				Name: textIn(c.Texts, lang, c.Languages, c.ID)})
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// textIn answers a text map's wording for lang, else the first declared language
// that has one, else fallback.
func textIn(texts map[string]string, lang string, langs []string, fallback string) string {
	if t := strings.TrimSpace(texts[lang]); t != "" {
		return t
	}
	for _, l := range langs {
		if t := strings.TrimSpace(texts[l]); t != "" {
			return t
		}
	}
	return fallback
}

// HandleTeamReport answers the caller's direct reports' rights and positions.
func (s *Service) HandleTeamReport(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	var me string
	if p != nil {
		me = p.UserID
	}
	team, err := s.directReports(me)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "team report: "+err.Error())
		return
	}
	if len(team) == 0 {
		httpapi.Error(w, http.StatusNotFound, "team report: no account names you as its line manager")
		return
	}
	// Every catalogue: a team is not a catalogue's audience, and what somebody holds
	// may come from any of them.
	cats, err := s.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "team report: "+err.Error())
		return
	}
	s.answerPeopleReport(w, r, "team", nil, cats, team)
}

// HandleOrganisationReport answers a catalogue's audience's rights and positions
// to one of its customer responsibles.
func (s *Service) HandleOrganisationReport(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	id := strings.TrimSpace(r.URL.Query().Get("catalog"))
	cats, err := s.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "organisation report: "+err.Error())
		return
	}
	var cat *catalog.Catalog
	for i := range cats {
		if cats[i].ID == id {
			cat = &cats[i]
		}
	}
	// Unknown and not yours read the same: whether a catalogue exists is not
	// something this confirms to somebody it does not report to.
	if cat == nil || p == nil || !cat.ReportsTo(p.GroupIDs) {
		httpapi.Error(w, http.StatusNotFound, "organisation report: you are not a customer responsible of catalogue "+id)
		return
	}
	people, err := s.audienceOf(*cat, cats)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "organisation report: "+err.Error())
		return
	}
	s.answerPeopleReport(w, r, "organisation", cat, []catalog.Catalog{*cat}, people)
}

// answerPeopleReport reads, audits and answers a report that names people.
func (s *Service) answerPeopleReport(w http.ResponseWriter, r *http.Request, scope string,
	cat *catalog.Catalog, cats []catalog.Catalog, people []string) {

	now := time.Unix(0, s.Now())
	fromNS, toNS, from, to, msg := reportWindow(r, now, s.loc())
	if msg != "" {
		httpapi.Error(w, http.StatusBadRequest, scope+" report: "+msg)
		return
	}
	lang := reportLang(r)
	sc, err := s.scopeOf(cats, lang)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, scope+" report: "+err.Error())
		return
	}
	who := make(map[string]bool, len(people))
	for _, id := range people {
		who[id] = true
	}
	read, err := s.measureScope(sc, fromNS, toNS, who)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, scope+" report: "+err.Error())
		return
	}
	nameOf := s.NameOf()
	priceOf := func(orderID, itemID, variantID string) string {
		o, ok := read.orders[orderID]
		if !ok {
			return ""
		}
		for _, l := range o.Lines {
			if l.ItemID == itemID && l.VariantID == variantID {
				return l.Price
			}
		}
		return ""
	}
	rows := People(people, nameOf, read.held, sc.names, priceOf, read.positions)

	attrs := []slog.Attr{slog.String("view", scope), slog.Int("people", len(people)),
		slog.String("from", from), slog.String("to", to)}
	if cat != nil {
		attrs = append(attrs, slog.String("catalog", cat.ID))
	}
	format := r.URL.Query().Get("format")
	if format == "csv" {
		attrs = append(attrs, slog.String("format", "csv"))
	}
	s.Audit(r, attrs...)

	if format == "csv" {
		var b bytes.Buffer
		var werr error
		kind := r.URL.Query().Get("kind")
		if kind == "positions" {
			werr = WritePositionsCSV(&b, read.positions, nameOf, sc.names, s.loc())
		} else {
			kind = "bezuege"
			werr = WriteHoldingsCSV(&b, rows, s.loc())
		}
		if werr != nil {
			httpapi.Error(w, http.StatusInternalServerError, scope+" report: "+werr.Error())
			return
		}
		writeCSV(w, scope+"-"+kind+"-"+from+"-"+to+".csv", b.Bytes())
		return
	}

	positions := make([]namedPosition, 0, len(read.positions))
	for _, p := range read.positions {
		name := sc.names[p.ItemID]
		if name == "" {
			name = p.ItemID
		}
		positions = append(positions, namedPosition{Position: p, RecipientName: nameOf(p.Recipient),
			OrdererName: nameOf(p.Orderer), Name: name})
	}
	sort.SliceStable(positions, func(i, j int) bool { return positions[i].OrderedAt > positions[j].OrderedAt })
	// The product rows count what these people hold, not everybody's.
	held := map[string]int{}
	for _, h := range read.held {
		held[h.ItemID]++
	}
	products := Summarise(read.positions, sc.names, held)
	rep := peopleReport{Scope: scope, From: from, To: to, People: rows, Positions: positions,
		Products: products, Totals: Total(products), GeneratedAt: now.UnixNano()}
	if cat != nil {
		rep.Catalog, rep.CatalogName = cat.ID, textIn(cat.Texts, lang, cat.Languages, cat.ID)
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/servicereport"
	"github.com/pblumer/atlas/logging"
)

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
	Scope       string                     `json:"scope"`
	Catalog     string                     `json:"catalog,omitempty"`
	CatalogName string                     `json:"catalogName,omitempty"`
	From        string                     `json:"from"`
	To          string                     `json:"to"`
	People      []servicereport.PersonRow  `json:"people"`
	Positions   []namedPosition            `json:"positions"`
	Products    []servicereport.ProductRow `json:"products"`
	Totals      servicereport.Totals       `json:"totals"`
	GeneratedAt int64                      `json:"generatedAt"`
}

// namedPosition is a position with the people it concerns by name and the product
// by its name.
type namedPosition struct {
	servicereport.Position
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
func (s *Server) directReports(manager string) ([]string, error) {
	if manager == "" || s.users == nil {
		return nil, nil
	}
	all, err := s.users.LoadAll()
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
func (s *Server) audienceOf(cat catalog.Catalog, cats []catalog.Catalog) ([]string, error) {
	if s.users == nil || s.groups == nil {
		return nil, nil
	}
	groups, err := s.groups.LoadAll()
	if err != nil {
		return nil, err
	}
	memberOf := map[string][]string{}
	for _, g := range groups {
		for _, m := range g.Members {
			memberOf[m] = append(memberOf[m], g.ID)
		}
	}
	users, err := s.users.LoadAll()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range users {
		if u.Disabled {
			continue
		}
		if got, ok := catalog.Resolve(cats, memberOf[u.ID]); ok && got.ID == cat.ID {
			out = append(out, u.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// handleMyReports answers which of the two reports the caller may open, so the
// shop draws a tab for each and none for the rest.
func (s *Server) handleMyReports(w http.ResponseWriter, r *http.Request) {
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
	cats, err := s.catalogStore.Catalogs()
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

// handleTeamReport answers the caller's direct reports' rights and positions.
func (s *Server) handleTeamReport(w http.ResponseWriter, r *http.Request) {
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
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "team report: "+err.Error())
		return
	}
	s.answerPeopleReport(w, r, "team", nil, cats, team)
}

// handleOrganisationReport answers a catalogue's audience's rights and positions
// to one of its customer responsibles.
func (s *Server) handleOrganisationReport(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	id := strings.TrimSpace(r.URL.Query().Get("catalog"))
	cats, err := s.catalogStore.Catalogs()
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
func (s *Server) answerPeopleReport(w http.ResponseWriter, r *http.Request, scope string,
	cat *catalog.Catalog, cats []catalog.Catalog, people []string) {

	now := time.Unix(0, s.now())
	fromNS, toNS, from, to, msg := reportWindow(r, now, time.Local)
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
	nameOf := s.principalNamer()
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
	rows := servicereport.People(people, nameOf, read.held, sc.names, priceOf, read.positions)

	attrs := []slog.Attr{slog.String("view", scope), slog.Int("people", len(people)),
		slog.String("from", from), slog.String("to", to)}
	if cat != nil {
		attrs = append(attrs, slog.String("catalog", cat.ID))
	}
	format := r.URL.Query().Get("format")
	if format == "csv" {
		attrs = append(attrs, slog.String("format", "csv"))
	}
	audit(r, logging.ReportNamesRead, "read a report that names people", attrs...)

	if format == "csv" {
		var b bytes.Buffer
		var werr error
		kind := r.URL.Query().Get("kind")
		if kind == "positions" {
			werr = servicereport.WritePositionsCSV(&b, read.positions, nameOf, sc.names, time.Local)
		} else {
			kind = "bezuege"
			werr = servicereport.WriteHoldingsCSV(&b, rows, time.Local)
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
	products := servicereport.Summarise(read.positions, sc.names, held)
	rep := peopleReport{Scope: scope, From: from, To: to, People: rows, Positions: positions,
		Products: products, Totals: servicereport.Total(products), GeneratedAt: now.UnixNano()}
	if cat != nil {
		rep.Catalog, rep.CatalogName = cat.ID, textIn(cat.Texts, lang, cat.Languages, cat.ID)
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

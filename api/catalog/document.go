package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue document is a shop as one file
// (ADR-draft-a-catalogue-is-imported-as-one-document): the catalogues, the products
// they maintain and offer, and the edges between them. It is what an example ships,
// and what moves a shop from one installation to another without a person
// re-entering it product by product.
//
// Importing one is the same set of writes the Console makes one at a time —
// catalogues, then products, then, if asked, a release of each catalogue — under
// the same authority, with one difference that is the reason it exists: it is all or
// nothing. Every catalogue, every product, every authority check and, when the
// document asks to be published, every publish problem is settled before the first
// record is written. A document that would leave a catalogue half-built is refused
// whole, with every problem at once.

// Document is the importable shape. IDs are the document's own and stable, so
// importing the same document again updates what the first import created rather
// than creating it twice.
type Document struct {
	Catalogs []Catalog `json:"catalogs"`
	Products []Item    `json:"products"`
	// Publish asks for a release of every catalogue in the document once it is
	// written, validated before anything is.
	Publish bool `json:"publish,omitempty"`
}

// DocumentResult is what an import answers: which catalogues and products it created
// and which it updated, and the releases it published.
type DocumentResult struct {
	Created  []string  `json:"created"`
	Updated  []string  `json:"updated"`
	Releases []Release `json:"releases,omitempty"`
}

// DocumentProblem is one reason a document is refused, naming what it is about.
type DocumentProblem struct {
	// Subject is "catalog:<id>" or "product:<id>", or "document" for the whole.
	Subject string `json:"subject"`
	Problem string `json:"problem"`
}

// documentID is what an id in a document may be: a word a URL carries without
// escaping, as every id the Console mints is.
var documentID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Check reports what is wrong with a document on its own, without asking the store:
// ids, languages, duplicates, products without a home, and the fields a document
// may not set. It is what an example's test runs, and the first thing an import does.
func (d Document) Check() []DocumentProblem {
	var out []DocumentProblem
	add := func(subject, format string, a ...any) {
		out = append(out, DocumentProblem{Subject: subject, Problem: fmt.Sprintf(format, a...)})
	}
	if len(d.Catalogs) == 0 && len(d.Products) == 0 {
		add("document", "the document names no catalogue and no product")
	}
	cats := map[string]bool{}
	for _, c := range d.Catalogs {
		subject := "catalog:" + c.ID
		switch {
		case !documentID.MatchString(c.ID):
			add(subject, "a catalogue needs an id of letters, digits, '.', '_' or '-', at most 100 long")
			continue
		case cats[c.ID]:
			add(subject, "the document names this catalogue twice")
			continue
		}
		cats[c.ID] = true
		if why := LanguageListProblem(c.Languages); why != "" {
			add(subject, "%s", why)
		}
		if c.Theme != (Theme{}) {
			add(subject, "a theme is set by an administrator with PUT /api/v1/catalogs/{id}/theme, not by an import")
		}
	}
	products := map[string]bool{}
	for _, it := range d.Products {
		subject := "product:" + it.ID
		switch {
		case !documentID.MatchString(it.ID):
			add(subject, "a product needs an id of letters, digits, '.', '_' or '-', at most 100 long")
			continue
		case products[it.ID]:
			add(subject, "the document names this product twice")
			continue
		}
		products[it.ID] = true
		if it.HomeCatalog == "" {
			add(subject, "a product needs a homeCatalog: the catalogue that maintains it")
		}
	}
	return out
}

// HandleImportDocument serves POST /api/v1/catalogs/import.
func (s *Service) HandleImportDocument(w http.ResponseWriter, r *http.Request) {
	var doc Document
	if err := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Import)).Decode(&doc); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	if problems := doc.Check(); len(problems) > 0 {
		httpapi.JSON(w, http.StatusBadRequest, map[string]any{"problems": problems})
		return
	}
	p := httpapi.PrincipalFrom(r.Context())

	var (
		plan     importPlan
		problems []DocumentProblem
		opErr    error
	)
	s.loop.Do(func() { plan, problems, opErr = s.planImport(doc, p) })
	if opErr == nil && len(problems) == 0 && doc.Publish {
		problems = s.publishProblems(plan)
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import: "+opErr.Error())
		return
	case len(problems) > 0:
		code := http.StatusUnprocessableEntity
		for _, pr := range problems {
			if pr.Problem == problemNotYours {
				code = http.StatusForbidden
			}
		}
		httpapi.JSON(w, code, map[string]any{"problems": problems})
		return
	}

	// The writes go in a second visit to the loop, which plans again: what was
	// checked a moment ago is checked again where nothing can change under it, so a
	// catalogue edited in between is neither overwritten blind nor half-imported. Only
	// whether the bound processes are deployed is not asked again — that is the
	// server's to answer on a visit of its own, as a single publish asks it.
	var result DocumentResult
	s.loop.Do(func() { result, problems, opErr = s.applyImport(doc, p) })
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import: "+opErr.Error())
	case len(problems) > 0:
		httpapi.JSON(w, http.StatusConflict, map[string]any{"problems": problems})
	default:
		httpapi.JSON(w, http.StatusOK, result)
	}
}

// problemNotYours is the problem an import is refused with for want of authority,
// which the response answers 403 rather than 422.
const problemNotYours = "you do not maintain this catalogue"

// importPlan is a document resolved against the store: the records as they will be
// written, and which of them exist already.
type importPlan struct {
	catalogs map[string]Catalog
	products map[string]Item
	existing map[string]bool // "catalog:<id>" and "product:<id>"
	order    []string        // the document's catalogues, in its order
	items    []string        // the document's products, in its order
}

// planImport resolves a document against the store and checks every authority, on
// the run loop. Nothing is written.
func (s *Service) planImport(doc Document, p *httpapi.Principal) (importPlan, []DocumentProblem, error) {
	plan := importPlan{catalogs: map[string]Catalog{}, products: map[string]Item{}, existing: map[string]bool{}}
	var problems []DocumentProblem
	add := func(subject, problem string) {
		problems = append(problems, DocumentProblem{Subject: subject, Problem: problem})
	}
	now := s.now()
	for _, in := range doc.Catalogs {
		subject := "catalog:" + in.ID
		prev, found, err := s.store.Catalog(in.ID)
		if err != nil {
			return plan, nil, err
		}
		next := in
		next.Theme, next.OwnerID = Theme{}, ""
		if found {
			// The same authority a PATCH needs, and sharing stays the owner's.
			if !s.mayRead(prev, p) || !s.mayEdit(prev, p) {
				add(subject, problemNotYours)
				continue
			}
			if !sameMembers(prev.Members, in.Members) && !s.mayShare(prev, p) {
				add(subject, "changing who maintains this catalogue is the owner's; an editor may import it with its members unchanged")
				continue
			}
			next.Theme, next.OwnerID, next.CreatedAt = prev.Theme, prev.OwnerID, prev.CreatedAt
			next.Revision = prev.Revision + 1
			plan.existing[subject] = true
		} else {
			if p != nil {
				next.OwnerID = p.UserID
			}
			next.CreatedAt, next.Revision = now, 1
		}
		next.UpdatedAt = now
		plan.catalogs[in.ID] = next
		plan.order = append(plan.order, in.ID)
	}

	// homeOf answers what the caller may do with a catalogue as a product's home: one
	// the document carries is being written by this caller; one on the server must be
	// seen and maintained by them. A catalogue they cannot see is reported as one they
	// may not write — never as absent, which would let a product be taken from it.
	homeOf := func(id string) (exists, visible, allowed bool, err error) {
		if _, inDoc := plan.catalogs[id]; inDoc {
			return true, true, true, nil
		}
		c, found, err := s.store.Catalog(id)
		if err != nil || !found {
			return false, false, false, err
		}
		visible = s.mayRead(c, p)
		return true, visible, visible && s.mayEdit(c, p), nil
	}
	for _, in := range doc.Products {
		subject := "product:" + in.ID
		exists, visible, allowed, err := homeOf(in.HomeCatalog)
		if err != nil {
			return plan, nil, err
		}
		if !exists || !visible {
			// A home the caller cannot see reads as absent, as a single save says.
			add(subject, "no home catalogue "+in.HomeCatalog+" in the document or on this server")
			continue
		}
		if !allowed {
			add(subject, problemNotYours)
			continue
		}
		prev, found, err := s.store.Item(in.ID)
		if err != nil {
			return plan, nil, err
		}
		next := in
		if found {
			// Moving a product takes it from the catalogue responsible for it now,
			// so that side must agree too, as it must for a single save.
			if prev.HomeCatalog != in.HomeCatalog {
				if exists, _, allowed, err := homeOf(prev.HomeCatalog); err != nil {
					return plan, nil, err
				} else if exists && !allowed {
					add(subject, problemNotYours)
					continue
				}
			}
			next.CreatedAt, next.Revision = prev.CreatedAt, prev.Revision+1
			plan.existing[subject] = true
		} else {
			next.CreatedAt, next.Revision = now, 1
		}
		next.UpdatedAt = now
		plan.products[in.ID] = next
		plan.items = append(plan.items, in.ID)
	}

	// A catalogue cannot offer a product nobody has, as a PATCH may not.
	for _, id := range plan.order {
		for _, item := range plan.catalogs[id].Items {
			if _, inDoc := plan.products[item]; inDoc {
				continue
			}
			if _, found, err := s.store.Item(item); err != nil {
				return plan, nil, err
			} else if !found {
				add("catalog:"+id, "offers "+item+", which is neither in the document nor on this server")
			}
		}
	}
	return plan, problems, nil
}

// publishProblems runs the publish checks of every catalogue in the plan against the
// store as it will be once the plan is written, before it is.
func (s *Service) publishProblems(plan importPlan) []DocumentProblem {
	var (
		out   []DocumentProblem
		rels  []Release
		opErr error
	)
	s.loop.Do(func() {
		var inputs []Input
		if inputs, opErr = s.plannedInputs(plan); opErr != nil {
			return
		}
		for _, in := range inputs {
			rel, problems := Publish(in)
			for _, pr := range problems {
				out = append(out, DocumentProblem{Subject: "catalog:" + in.CatalogID, Problem: pr.String()})
			}
			rels = append(rels, rel)
		}
	})
	if opErr != nil {
		return []DocumentProblem{{Subject: "document", Problem: "read the catalogue store: " + opErr.Error()}}
	}
	if len(out) > 0 {
		return out
	}
	for i, rel := range rels {
		for _, pr := range LifecycleProblems(rel.Items, s.EntryPoints) {
			out = append(out, DocumentProblem{Subject: "catalog:" + plan.order[i], Problem: pr.String()})
		}
	}
	return out
}

// plannedInputs is InputFor for each of the plan's catalogues, read through the plan:
// the store's records with the document's laid over them.
func (s *Service) plannedInputs(plan importPlan) ([]Input, error) {
	stored, err := s.store.Catalogs()
	if err != nil {
		return nil, err
	}
	var all []Catalog
	for _, c := range stored {
		if _, replaced := plan.catalogs[c.ID]; !replaced {
			all = append(all, c)
		}
	}
	for _, id := range plan.order {
		all = append(all, plan.catalogs[id])
	}
	items, err := s.store.Items()
	if err != nil {
		return nil, err
	}
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	for id, it := range plan.products {
		byID[id] = it
	}
	var out []Input
	for _, id := range plan.order {
		cat := plan.catalogs[id]
		var carried []Item
		for _, item := range cat.Items {
			if it, ok := byID[item]; ok {
				carried = append(carried, it)
			}
		}
		sort.Slice(carried, func(a, b int) bool { return carried[a].ID < carried[b].ID })
		out = append(out, Input{CatalogID: id, Catalogs: all, Items: carried, Edges: cat.Edges})
	}
	return out, nil
}

// applyImport plans the document again and writes it: the catalogues, then the
// products, then — when asked — a release of each catalogue, computed before the
// first write from the store as the writes will leave it. Runs on the run loop, so
// nothing changes between the check and the write.
func (s *Service) applyImport(doc Document, p *httpapi.Principal) (DocumentResult, []DocumentProblem, error) {
	result := DocumentResult{Created: []string{}, Updated: []string{}}
	plan, problems, err := s.planImport(doc, p)
	if err != nil || len(problems) > 0 {
		return result, problems, err
	}
	var rels []Release
	if doc.Publish {
		inputs, err := s.plannedInputs(plan)
		if err != nil {
			return result, nil, err
		}
		for _, in := range inputs {
			rel, found := Publish(in)
			for _, pr := range found {
				problems = append(problems, DocumentProblem{Subject: "catalog:" + in.CatalogID, Problem: pr.String()})
			}
			rels = append(rels, rel)
		}
		if len(problems) > 0 {
			return result, problems, nil
		}
	}
	note := func(subject string) {
		if plan.existing[subject] {
			result.Updated = append(result.Updated, subject)
		} else {
			result.Created = append(result.Created, subject)
		}
	}
	for _, id := range plan.order {
		if err := s.store.SaveCatalog(plan.catalogs[id]); err != nil {
			return result, nil, err
		}
		note("catalog:" + id)
	}
	for _, id := range plan.items {
		if err := s.store.SaveItem(plan.products[id]); err != nil {
			return result, nil, err
		}
		note("product:" + id)
	}
	for i, rel := range rels {
		relID, err := newID("rel")
		if err != nil {
			return result, nil, err
		}
		rel.ID, rel.CatalogID, rel.CreatedAt = relID, plan.order[i], s.now()
		if err := s.store.SaveRelease(rel); err != nil {
			return result, nil, err
		}
		result.Releases = append(result.Releases, rel)
	}
	return result, nil, nil
}

// sameMembers reports whether two member lists grant the same, in any order.
func sameMembers(a, b []Member) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(m Member) string { return m.Ref.Type + "\x00" + m.Ref.ID + "\x00" + string(m.Role) }
	seen := map[string]int{}
	for _, m := range a {
		seen[key(m)]++
	}
	for _, m := range b {
		if seen[key(m)] == 0 {
			return false
		}
		seen[key(m)]--
	}
	return true
}

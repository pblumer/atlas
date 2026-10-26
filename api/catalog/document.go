package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// A catalogue document is a shop as one file
// (ADR-0436): the catalogues, the products
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
	// Edges are the structure and precedence between the document's products when
	// it carries products alone (ADR-0451). Edges belong to a
	// catalogue, so these land in the catalogue the products are imported into
	// (?into=), and a document carrying them is refused without one.
	Edges []Edge `json:"edges,omitempty"`
	// Processes are the BPMN models the products are bound to, deployed by the
	// import before the catalogues are written (ADR-0451).
	Processes []Process `json:"processes,omitempty"`
	// MissingProcesses names the processes an export was asked to carry and could
	// not, because the exporting server has none deployed under that id. An import
	// reads nothing from it; it is there for the person holding the file.
	MissingProcesses []string `json:"missingProcesses,omitempty"`
}

// Process is one BPMN model a document carries: the process id a product binds,
// and the XML the newest version of it was deployed from.
type Process struct {
	ID  string `json:"id"`
	XML string `json:"xml"`
}

// DocumentResult is what an import answers: which catalogues and products it created
// and which it updated, and the releases it published.
type DocumentResult struct {
	Created  []string  `json:"created"`
	Updated  []string  `json:"updated"`
	Releases []Release `json:"releases,omitempty"`
	// Warnings are what the releases leave exposed (AnswerWarnings), by product.
	Warnings []DocumentProblem `json:"warnings,omitempty"`
	// Deployed names the document's processes this import deployed, and Unchanged
	// those whose newest deployed version already was the document's model.
	Deployed  []string `json:"deployed,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
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
func (d Document) Check() []DocumentProblem { return d.check("") }

// check is Check for a document imported into the catalogue into, or into none when
// it is empty. Imported into one, a product needs no home of its own — the import
// gives it one — and the document may carry no catalogue.
func (d Document) check(into string) []DocumentProblem {
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
		if it.HomeCatalog == "" && into == "" {
			add(subject, "a product needs a homeCatalog: the catalogue that maintains it")
		}
	}
	for _, e := range d.Edges {
		for _, end := range []string{e.From, e.To} {
			if !products[end] {
				add("edge:"+e.From+"->"+e.To, "names %s, which is not one of the document's products", end)
			}
		}
	}
	procs := map[string]bool{}
	for _, pr := range d.Processes {
		subject := "process:" + pr.ID
		switch {
		case strings.TrimSpace(pr.ID) == "":
			add("document", "a process needs an id: the process id a product binds")
		case procs[pr.ID]:
			add(subject, "the document names this process twice")
		case strings.TrimSpace(pr.XML) == "":
			add(subject, "the process carries no model")
		}
		procs[pr.ID] = true
	}
	switch {
	case into != "" && !documentID.MatchString(into):
		add("document", "into must name a catalogue id of letters, digits, '.', '_' or '-'")
	case into != "" && len(d.Catalogs) > 0:
		add("document", "a document imported into a catalogue carries no catalogue of its own: its products land in %s", into)
	case into == "" && len(d.Edges) > 0:
		add("document", "edges outside a catalogue land in the catalogue the products are imported into: name it with ?into=")
	}
	return out
}

// HandleImportDocument serves POST /api/v1/catalogs/import.
//
// ?into=<catalogue> imports a document of products alone into a catalogue that
// exists (ADR-0451): every product is offered there, a product new
// to this server is homed there, and the document's edges are added to it.
func (s *Service) HandleImportDocument(w http.ResponseWriter, r *http.Request) {
	var doc Document
	if err := json.NewDecoder(io.LimitReader(r.Body, s.budgets().Import)).Decode(&doc); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
		return
	}
	into := r.URL.Query().Get("into")
	if problems := doc.check(into); len(problems) > 0 {
		httpapi.JSON(w, http.StatusBadRequest, map[string]any{"problems": problems})
		return
	}
	p := httpapi.PrincipalFrom(r.Context())

	var (
		plan     importPlan
		problems []DocumentProblem
		opErr    error
	)
	s.loop.Do(func() { plan, problems, opErr = s.resolveImport(doc, into, p) })
	if opErr == nil && len(doc.Processes) > 0 {
		// Asked off the loop: the deployer answers for the server, and reaches its
		// state through the loop itself.
		if s.Deployer == nil {
			problems = append(problems, DocumentProblem{Subject: "document",
				Problem: "this server cannot deploy a document's processes; import it without them"})
		} else {
			problems = append(problems, s.Deployer.CheckProcesses(r, doc.Processes)...)
		}
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "import: "+opErr.Error())
		return
	}
	if len(problems) > 0 {
		refuseImport(w, problems)
		return
	}

	// The processes go first, because publishing asks whether what the products are
	// bound to is deployed. This is the one step an import cannot take back: a
	// catalogue refused after it leaves the processes deployed, which the answer says.
	var result DocumentResult
	if len(doc.Processes) > 0 {
		deployed, unchanged, refused, err := s.Deployer.DeployProcesses(r, doc.Processes)
		switch {
		case err != nil:
			httpapi.Error(w, http.StatusInternalServerError, "import: deploy processes: "+err.Error())
			return
		case len(refused) > 0:
			httpapi.JSON(w, http.StatusUnprocessableEntity, map[string]any{"problems": refused, "deployed": deployed})
			return
		}
		result.Deployed, result.Unchanged = deployed, unchanged
	}
	if doc.Publish {
		if problems = s.publishProblems(plan); len(problems) > 0 {
			refuseImport(w, problems, result.Deployed...)
			return
		}
	}

	// The writes go in a second visit to the loop, which plans again: what was
	// checked a moment ago is checked again where nothing can change under it, so a
	// catalogue edited in between is neither overwritten blind nor half-imported. Only
	// whether the bound processes are deployed is not asked again — that is the
	// server's to answer on a visit of its own, as a single publish asks it.
	var written DocumentResult
	s.loop.Do(func() { written, problems, opErr = s.applyImport(doc, into, p) })
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import: "+opErr.Error())
	case len(problems) > 0:
		httpapi.JSON(w, http.StatusConflict, map[string]any{"problems": problems, "deployed": result.Deployed})
	default:
		written.Deployed, written.Unchanged = result.Deployed, result.Unchanged
		for _, rel := range written.Releases {
			for _, wn := range AnswerWarnings(rel.Items, s.Forms, s.Personal) {
				written.Warnings = append(written.Warnings, DocumentProblem{Subject: "product:" + wn.Item, Problem: wn.Message})
			}
		}
		httpapi.JSON(w, http.StatusOK, written)
	}
}

// refuseImport answers a refused document: 403 when any problem is one of
// authority, 422 otherwise. deployed names processes an earlier step of the same
// import already deployed, which a refusal must not hide.
func refuseImport(w http.ResponseWriter, problems []DocumentProblem, deployed ...string) {
	code := http.StatusUnprocessableEntity
	for _, pr := range problems {
		if pr.Problem == problemNotYours || pr.Problem == ProblemMayNotDeploy {
			code = http.StatusForbidden
		}
	}
	body := map[string]any{"problems": problems}
	if len(deployed) > 0 {
		body["deployed"] = deployed
	}
	httpapi.JSON(w, code, body)
}

// ProblemMayNotDeploy is the problem a document carrying processes is refused with
// when its importer may not deploy them, which the response answers 403.
const ProblemMayNotDeploy = "deploying a document's processes needs the modeler role"

// resolveImport is planImport for a document imported into a catalogue or into none.
// Runs on the run loop.
func (s *Service) resolveImport(doc Document, into string, p *httpapi.Principal) (importPlan, []DocumentProblem, error) {
	if into != "" {
		retargeted, problems, err := s.retarget(doc, into, p)
		if err != nil || len(problems) > 0 {
			return importPlan{}, problems, err
		}
		doc = retargeted
	}
	return s.planImport(doc, p)
}

// retarget turns a document of products into one that writes them into the
// catalogue into: the catalogue as stored, offering every product and carrying the
// document's edges, and each product homed there — unless this server already
// has it, in which case it keeps the home it has. Importing a product into a
// second catalogue offers it there; it does not take it from the first, which a
// product referenced by several catalogues must not be. Runs on the run loop.
func (s *Service) retarget(doc Document, into string, p *httpapi.Principal) (Document, []DocumentProblem, error) {
	c, found, err := s.store.Catalog(into)
	if err != nil {
		return doc, nil, err
	}
	if !found || !s.mayRead(c, p) {
		// A catalogue the caller cannot see reads as absent, as it does everywhere.
		return doc, []DocumentProblem{{Subject: "catalog:" + into, Problem: "no catalogue " + into + " on this server"}}, nil
	}
	if !s.mayEdit(c, p) {
		return doc, []DocumentProblem{{Subject: "catalog:" + into, Problem: problemNotYours}}, nil
	}
	next := c
	next.Items = append([]string(nil), c.Items...)
	next.Edges = append([]Edge(nil), c.Edges...)
	products := make([]Item, len(doc.Products))
	for i, it := range doc.Products {
		prev, found, err := s.store.Item(it.ID)
		if err != nil {
			return doc, nil, err
		}
		it.HomeCatalog = into
		if found {
			it.HomeCatalog = prev.HomeCatalog
		}
		products[i] = it
		if !slices.Contains(next.Items, it.ID) {
			next.Items = append(next.Items, it.ID)
		}
	}
	for _, e := range doc.Edges {
		if !slices.Contains(next.Edges, e) {
			next.Edges = append(next.Edges, e)
		}
	}
	doc.Catalogs, doc.Products, doc.Edges = []Catalog{next}, products, nil
	return doc, nil, nil
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
		// While bound, what a source supplies is not an import's to change
		// either; an import that carries the product unchanged keeps it.
		next, bound := boundSave(prev, found, in)
		if bound != "" {
			add(subject, bound)
			continue
		}
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
		for _, pr := range append(LifecycleProblems(rel.Items, s.EntryPoints), OrderFormProblems(rel.Items, s.Forms)...) {
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
func (s *Service) applyImport(doc Document, into string, p *httpapi.Principal) (DocumentResult, []DocumentProblem, error) {
	result := DocumentResult{Created: []string{}, Updated: []string{}}
	plan, problems, err := s.resolveImport(doc, into, p)
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

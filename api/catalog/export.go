package catalog

import (
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// Exporting a catalogue (ADR-0451) is the other half of importing
// one (ADR-0436): it answers the same Document the import takes, so what a product
// manager built in the Console on one installation becomes a file they review and
// import on another.
//
// Two shapes, for two questions:
//
//   - a whole catalogue — the catalogue with every product it maintains or offers,
//     imported as it is;
//   - a choice of products — the products and the edges between them, without a
//     catalogue, imported into whichever catalogue the importer names (?into=).
//
// Either may carry the processes the products are bound to. A catalogue binds a
// product to a process by id and the process lives in the deployment registry, not
// here, so a catalogue moved without them arrives bound to processes the target may
// not have. They travel as the BPMN their newest version was deployed from, and the
// import deploys them through the server's own deploy door before it writes the
// catalogue.
//
// What an export leaves out is what belongs to the installation and not to the
// catalogue: the owner (an account id that means nothing elsewhere), revisions and
// times (the import assigns its own), and the theme, logo and pictures, which a
// document does not carry (ADR-0436).

// ProcessSource answers the BPMN the newest deployed version of a process id was
// deployed from. It belongs to the server, which owns the deployments; nil refuses
// an export asked to carry processes.
type ProcessSource interface {
	ProcessXML(processID string) (xml []byte, deployed bool)
}

// ProcessDeployer deploys a document's processes as the person importing it. It
// belongs to the server for ProcessSource's reason; nil refuses a document that
// carries processes.
type ProcessDeployer interface {
	// CheckProcesses reports, without deploying anything, every process the
	// caller may not deploy and every one that would not deploy. A refusal for
	// want of the role is ProblemMayNotDeploy.
	CheckProcesses(r *http.Request, procs []Process) []DocumentProblem
	// DeployProcesses deploys every process whose newest deployed version is not
	// already the document's model, through the same door a single deploy takes,
	// and names which it deployed and which it left unchanged.
	DeployProcesses(r *http.Request, procs []Process) (deployed, unchanged []string, problems []DocumentProblem, err error)
}

// HandleExportCatalog serves GET /api/v1/catalogs/{id}/document: the catalogue as a
// document, ?processes=true with the processes its products are bound to.
//
// Only a maintainer exports. The document is the whole record — members, approval
// rules, process bindings — which is more than the shop shows the people a
// catalogue is for, so seeing the catalogue is not enough. A catalogue the caller
// cannot see reads as absent, as it does everywhere.
func (s *Service) HandleExportCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())
	var (
		doc             Document
		found, readable bool
		allowed         bool
		loadErr         error
	)
	s.loop.Do(func() {
		var c Catalog
		if c, found, loadErr = s.store.Catalog(id); loadErr != nil || !found {
			return
		}
		if readable = s.mayRead(c, p); !readable {
			return
		}
		if allowed = s.mayEdit(c, p); !allowed {
			return
		}
		var all []Item
		if all, loadErr = s.store.Items(); loadErr != nil {
			return
		}
		for _, it := range all {
			if it.HomeCatalog == id || slices.Contains(c.Items, it.ID) {
				var x Item
				if x, loadErr = s.exportable(it); loadErr != nil {
					return
				}
				doc.Products = append(doc.Products, x)
			}
		}
		c.OwnerID, c.Theme = "", Theme{}
		c.Revision, c.CreatedAt, c.UpdatedAt = 0, 0, 0
		doc.Catalogs = []Catalog{c}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "export catalogue: "+loadErr.Error())
	case !found || !readable:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, problemNotYours)
	default:
		s.answerExport(w, r, doc)
	}
}

// HandleExportProducts serves GET /api/v1/catalog-products/document?ids=a,b: the
// products named, and the edges of their home catalogues that join two of them,
// ?processes=true with the processes they are bound to.
//
// Each must be a product the caller maintains, which is what the product list shows
// them; one they do not reads as absent, as it does there.
func (s *Service) HandleExportProducts(w http.ResponseWriter, r *http.Request) {
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "name the products to export: ?ids=a,b")
		return
	}
	p := httpapi.PrincipalFrom(r.Context())
	var (
		doc     Document
		missing string
		loadErr error
	)
	s.loop.Do(func() {
		homes := map[string]Catalog{}
		for _, id := range ids {
			it, found, err := s.store.Item(id)
			if err != nil {
				loadErr = err
				return
			}
			c, known := homes[it.HomeCatalog]
			if found && !known {
				if c, known, err = s.store.Catalog(it.HomeCatalog); err != nil {
					loadErr = err
					return
				}
				homes[it.HomeCatalog] = c
			}
			if !found || !known || !s.mayEdit(c, p) {
				missing = id
				return
			}
			x, err := s.exportable(it)
			if err != nil {
				loadErr = err
				return
			}
			doc.Products = append(doc.Products, x)
		}
		// Each edge once, in the order the catalogues hold them, homes by id.
		var homeIDs []string
		for h := range homes {
			homeIDs = append(homeIDs, h)
		}
		sort.Strings(homeIDs)
		for _, h := range homeIDs {
			for _, e := range homes[h].Edges {
				if slices.Contains(ids, e.From) && slices.Contains(ids, e.To) && !slices.Contains(doc.Edges, e) {
					doc.Edges = append(doc.Edges, e)
				}
			}
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "export products: "+loadErr.Error())
	case missing != "":
		httpapi.Error(w, http.StatusNotFound, "no product "+missing)
	default:
		sort.Slice(doc.Products, func(a, b int) bool { return doc.Products[a].ID < doc.Products[b].ID })
		s.answerExport(w, r, doc)
	}
}

// answerExport adds the processes when asked and writes the document. The processes
// are read off the loop: the source reaches the server's deployments through the
// loop itself, and Loop.Do is a rendezvous.
func (s *Service) answerExport(w http.ResponseWriter, r *http.Request, doc Document) {
	if want := r.URL.Query().Get("processes"); want == "true" || want == "1" {
		if s.Models == nil {
			httpapi.Error(w, http.StatusServiceUnavailable, "this server cannot read its processes to export them")
			return
		}
		for _, id := range processesOf(doc.Products) {
			if xml, deployed := s.Models.ProcessXML(id); deployed {
				doc.Processes = append(doc.Processes, Process{ID: id, XML: string(xml)})
			} else {
				doc.MissingProcesses = append(doc.MissingProcesses, id)
			}
		}
	}
	httpapi.JSON(w, http.StatusOK, doc)
}

// exported is a product as a document carries it: without the revision and times
// the importing server assigns itself.
func exported(it Item) Item {
	it.Revision, it.CreatedAt, it.UpdatedAt = 0, 0, 0
	return it
}

// processesOf names, sorted and once each, the processes these products run on:
// the two bindings or the lifecycle process, and an approval process of the
// catalogue's own. Atlas's shipped approval models and its fulfilment orchestration
// are not among them — every installation has them, and a document carrying them
// would redeploy the server's own models over its own.
func processesOf(items []Item) []string {
	var out []string
	add := func(id string) {
		if id = strings.TrimSpace(id); id != "" && id != FulfilmentProcess && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, it := range items {
		add(it.ProvisionProcess)
		add(it.DeprovisionProcess)
		add(it.LifecycleProcess)
		if _, shipped := approvalProcessOf[string(it.Approval.Kind)]; !shipped {
			add(ApprovalProcessFor(it))
		}
	}
	sort.Strings(out)
	return out
}

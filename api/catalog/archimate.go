package catalog

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// Importing a catalogue from an ArchiMate model.
//
// The model already says what a product is made of, in the vocabulary an
// architect uses: a Product or a Business Service is something offered, a
// composition is a part that comes with the whole, an aggregation is one offered
// beside it. That is the same distinction this catalogue makes — which is the
// whole reason to read the model rather than retype a list of names.
//
// It is a starting point and not a synchronisation. Everything arrives as a
// draft, so importing makes nothing orderable, and a second import produces
// records a caller may compare with what is stored rather than writing over it.
//
// What it cannot take, it names. A silent drop is the failure mode of every
// importer: an architect who modelled something and does not find it has to be
// told which thing and why, or they conclude the import is broken and stop using
// it.

const (
	// maxImportDepth bounds nesting, so a document cannot exhaust the stack.
	maxImportDepth = 64
	// maxSkipped bounds the report. Past it the reader has the message anyway.
	maxSkipped = 200
)

// importable are the ArchiMate types a catalogue entry can come from. Product
// and Business Service are what an organisation offers; everything else in the
// model describes how it is delivered, which is not what is ordered.
var importable = map[string]bool{"Product": true, "BusinessService": true}

// importableEdge maps the two structural relationships onto the two the
// catalogue has. They already mean the same thing, which is why this mapping is
// one line rather than an interpretation.
var importableEdge = map[string]EdgeKind{
	"Composition": EdgeComposition,
	"Aggregation": EdgeAggregation,
}

// Import is what a model yielded: the drafts, the structure between them, and
// everything that did not come across.
type Import struct {
	Items []Item `json:"items"`
	Edges []Edge `json:"edges"`
	// Skipped names what was left out and why, in the model's own words where it
	// has them.
	Skipped []string `json:"skipped"`
}

// ImportArchiMate reads an ArchiMate Open Exchange document and derives
// catalogue drafts from it. The returned records are not stored; a caller
// compares them with what it has and decides.
func ImportArchiMate(data []byte, homeCatalog string) (Import, error) {
	out := Import{Items: []Item{}, Edges: []Edge{}, Skipped: []string{}}

	skip := func(format string, args ...any) {
		if len(out.Skipped) >= maxSkipped {
			return
		}
		out.Skipped = append(out.Skipped, fmt.Sprintf(format, args...))
	}

	type element struct {
		archiID string
		typ     string
		texts   map[string]string
	}
	type relation struct {
		archiID, source, target, typ string
	}

	var (
		elements  []element
		relations []relation
		cur       *element
		lang      string
		capture   *strings.Builder
		depth     int
		sawModel  bool
	)

	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Import{}, fmt.Errorf("catalog: invalid XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			// Same refusal panorama makes: a directive is where entity expansion
			// lives, and nothing in this format needs one.
			return Import{}, fmt.Errorf("catalog: XML directives are not allowed")
		case xml.StartElement:
			if depth++; depth > maxImportDepth {
				return Import{}, fmt.Errorf("catalog: the model nests deeper than %d", maxImportDepth)
			}
			if depth == 1 {
				// Say what the document is not. Reading something else and
				// answering "nothing imported" is the reply that sends somebody
				// looking for a bug in their model.
				if t.Name.Local != "model" {
					return Import{}, fmt.Errorf(
						"catalog: this is not an ArchiMate Model Exchange document — its root is <%s>, not <model>",
						t.Name.Local)
				}
				sawModel = true
			}
			switch t.Name.Local {
			case "element":
				cur = &element{
					archiID: attr(t, "identifier"),
					typ:     local(attr(t, "type")),
					texts:   map[string]string{},
				}
			case "relationship":
				relations = append(relations, relation{
					archiID: attr(t, "identifier"),
					source:  attr(t, "source"),
					target:  attr(t, "target"),
					typ:     local(attr(t, "type")),
				})
			case "name":
				if cur != nil {
					lang = attr(t, "lang")
					capture = &strings.Builder{}
				}
			}
		case xml.CharData:
			if capture != nil {
				capture.Write(t)
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "name":
				if cur != nil && capture != nil {
					if l := lang; l != "" {
						cur.texts[l] = strings.TrimSpace(capture.String())
					}
				}
				capture = nil
			case "element":
				if cur != nil {
					elements = append(elements, *cur)
					cur = nil
				}
			}
		}
	}

	// Elements first: an edge can only be kept once both its ends are known.
	byArchiID := map[string]string{}
	taken := map[string]string{}
	for _, e := range elements {
		if !importable[e.typ] {
			skip("%s is a %s, which describes how something is delivered rather than what is ordered",
				describe(e.texts, e.archiID), orUnknown(e.typ))
			continue
		}
		name := anyText(e.texts)
		if name == "" {
			skip("the %s %s has no name, and an id is derived from one", e.typ, e.archiID)
			continue
		}
		id := slug(name)
		if id == "" {
			skip("%q yields no usable id", name)
			continue
		}
		if other, clash := taken[id]; clash {
			// Merging two services would merge two orders. The architect resolves
			// it; the import will not guess.
			skip("%q and %q would both become %q — rename one in the model", other, name, id)
			continue
		}
		taken[id] = name
		byArchiID[e.archiID] = id
		out.Items = append(out.Items, Item{
			ID: id, HomeCatalog: homeCatalog, State: StateDraft,
			Texts: e.texts,
		})
	}

	for _, r := range relations {
		kind, ok := importableEdge[r.typ]
		if !ok {
			skip("relationship %s is a %s; only Composition and Aggregation say what a product is made of",
				r.archiID, orUnknown(r.typ))
			continue
		}
		from, fromOK := byArchiID[r.source]
		to, toOK := byArchiID[r.target]
		if !fromOK || !toOK {
			skip("relationship %s points at something the catalogue does not carry", r.archiID)
			continue
		}
		out.Edges = append(out.Edges, Edge{From: from, To: to, Kind: kind})
	}

	if !sawModel {
		return Import{}, fmt.Errorf("catalog: this is not an ArchiMate Model Exchange document — it contains no <model>")
	}

	sort.Slice(out.Items, func(a, b int) bool { return out.Items[a].ID < out.Items[b].ID })
	sort.Slice(out.Edges, func(a, b int) bool {
		if out.Edges[a].From != out.Edges[b].From {
			return out.Edges[a].From < out.Edges[b].From
		}
		return out.Edges[a].To < out.Edges[b].To
	})
	return out, nil
}

// attr reads one attribute by local name, ignoring its namespace.
func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// local strips a namespace prefix, so "archimate:Product" and "Product" read
// alike — the same normalisation panorama's binding reader makes.
func local(v string) string {
	if i := strings.LastIndex(v, ":"); i >= 0 {
		return v[i+1:]
	}
	return v
}

// anyText returns a name to derive an id from, preferring German and then
// English before falling back to whichever the model has — an id is internal,
// and having one at all matters more than which language named it.
func anyText(texts map[string]string) string {
	for _, l := range []string{"de", "en"} {
		if v := strings.TrimSpace(texts[l]); v != "" {
			return v
		}
	}
	keys := make([]string, 0, len(texts))
	for k := range texts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(texts[k]); v != "" {
			return v
		}
	}
	return ""
}

// slug turns a name into a readable, URL-safe id.
//
// Readable matters: the id appears in a catalogue's item list, in an order's
// lines and in a release, and an opaque one would make all three unreadable.
// URL-safe matters because it appears in a path.
//
// Only ASCII letters and digits survive. German umlauts and ß are spelled out,
// because dropping their diaeresis would give "Grösse" and "Grosse" the same id;
// other diacritics fold to their base letter, which is what a French or Spanish
// name expects. A name with no ASCII letter left yields nothing and is reported
// rather than turned into a row of dashes.
func slug(name string) string {
	var b strings.Builder
	lastDash := true
	emit := func(s string) {
		b.WriteString(s)
		lastDash = false
	}
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			emit(string(r))
		case spelledOut[r] != "":
			emit(spelledOut[r])
		case folded[r] != 0:
			emit(string(folded[r]))
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// spelledOut are the characters whose diaeresis carries meaning: dropping it
// would give "Grösse" and "Grosse" the same id, and they are different words.
var spelledOut = map[rune]string{'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss"}

// folded are diacritics that decorate a letter without changing which letter it
// is, so the base one is the right id.
var folded = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'å': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ø': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u',
	'ç': 'c', 'ñ': 'n', 'ý': 'y',
}

// describe names an element the way its model does, falling back to its id.
func describe(texts map[string]string, archiID string) string {
	if n := anyText(texts); n != "" {
		return fmt.Sprintf("%q", n)
	}
	return "element " + archiID
}

func orUnknown(typ string) string {
	if typ == "" {
		return "element with no type"
	}
	return typ
}

// HandleImport reads an ArchiMate model and files what it finds into a
// catalogue, as drafts.
//
// It needs the authority to change the catalogue, because that is what it does.
// It never overwrites a product that is already stored: the model is where a
// catalogue starts, not something it follows (decision b4), and a second import
// after somebody bound processes and activated a product must not undo that
// work. What it left alone it says, in the same report as what it could not take
// — both are things the person who ran it needs to know.
func (s *Service) HandleImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	// One byte past the budget, so an over-large document is refused rather than
	// silently truncated into a model that parses and is missing half its
	// products. The ceiling is the installation's, not this file's: a number
	// written here would be a budget nobody could find or configure.
	max := s.budgets().Import
	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if int64(len(data)) > max {
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the model exceeds the %d byte limit", max))
		return
	}
	imported, err := ImportArchiMate(data, id)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		found   bool
		allowed bool
		opErr   error
	)
	s.loop.Do(func() {
		var cat Catalog
		if cat, found, opErr = s.store.Catalog(id); opErr != nil || !found {
			return
		}
		if found = s.mayRead(cat, p); !found {
			return
		}
		if allowed = s.mayEdit(cat, p); !allowed {
			return
		}

		offered := map[string]bool{}
		for _, existing := range cat.Items {
			offered[existing] = true
		}
		kept := make([]Item, 0, len(imported.Items))
		for _, it := range imported.Items {
			_, exists, e := s.store.Item(it.ID)
			if e != nil {
				opErr = e
				return
			}
			if exists {
				imported.Skipped = append(imported.Skipped,
					fmt.Sprintf("%q is already in the catalogue and was left as it is — "+
						"the model is where a catalogue starts, not something it follows", it.ID))
				// Still offered by this catalogue: the model says it belongs here.
				offered[it.ID] = true
				continue
			}
			it.CreatedAt, it.UpdatedAt = s.now(), s.now()
			if opErr = s.store.SaveItem(it); opErr != nil {
				return
			}
			kept = append(kept, it)
			offered[it.ID] = true
		}
		imported.Items = kept

		items := make([]string, 0, len(offered))
		for itemID := range offered {
			items = append(items, itemID)
		}
		sort.Strings(items)
		cat.Items = items
		cat.Edges = mergeEdges(cat.Edges, imported.Edges)
		cat.UpdatedAt = s.now()
		opErr = s.store.SaveCatalog(cat)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of catalogue "+id)
	default:
		httpapi.JSON(w, http.StatusOK, imported)
	}
}

// mergeEdges adds the imported edges to the ones already stored, without
// duplicating any. An edge the model no longer has is kept: somebody may have
// drawn it here, and an import is not a synchronisation.
func mergeEdges(have, add []Edge) []Edge {
	seen := map[Edge]bool{}
	out := make([]Edge, 0, len(have)+len(add))
	for _, e := range append(append([]Edge{}, have...), add...) {
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].From != out[b].From {
			return out[a].From < out[b].From
		}
		if out[a].To != out[b].To {
			return out[a].To < out[b].To
		}
		return out[a].Kind < out[b].Kind
	})
	return out
}

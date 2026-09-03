package infomodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Importing a class diagram somebody else drew.
//
// ADR-0230 settled that Atlas *authors* a declared subset of the UML class diagram
// and *projects* it to JSON Schema and XMI, and it explicitly left reading a foreign
// tool's XMI out — "an export, not an interchange", until it was tested. This file is
// that follow-up (ADR-0232): a model is routinely drawn in a UML
// tool long before anybody opens Atlas, and retyping it by hand is both the slowest
// way to start and the one that quietly loses a business key.
//
// It reads two documents, and they answer two different needs:
//
//   - **JSON** — Atlas's own model shape, exactly what GET /models/{id} hands out. It
//     is how a model moves between applications and installations, and it round-trips
//     without loss because it is the native form.
//   - **XMI 2.5.1** — what a UML tool exports. It is a foreign document in a large
//     standard, so reading it is lossy by construction.
//
// The discipline is the one the projections already follow: **nothing is dropped
// silently**. Every element the subset does not author, every multiplicity outside
// it, every type that resolved to nothing becomes an ImportNote naming the element
// and saying what happened to it. The alternative — refusing the whole document
// because it contains an interface — would make the feature useless on every real
// model, and the alternative to *that* — importing quietly and letting the modeler
// discover the gaps — is worse than both.
//
// The second rule is that an import goes through the same door as a canvas write:
// what this file returns is always a model Validate accepts, so the store's guarantee
// (every model on disk is one the subset accepts, so a deploy resolving
// itemSubjectRef never meets a half-model) holds for an imported model too.

// The formats an import may arrive in.
const (
	ImportFormatJSON = "json"
	ImportFormatXMI  = "xmi"
)

// What became of one element of the source document. The three levels are what a
// reader acts on differently: something is gone, something is here but not as it was
// written, or something is worth knowing.
const (
	// NoteDropped: the element is not in the imported model at all.
	NoteDropped = "dropped"
	// NoteAdjusted: the element is here, saying something slightly different from
	// what the document said — a widened multiplicity, a type read as text.
	NoteAdjusted = "adjusted"
	// NoteInfo: nothing was lost; this is a fact about the import worth stating.
	NoteInfo = "info"
)

// ImportNote is one thing the import could not carry, or carried differently.
type ImportNote struct {
	Level string `json:"level"`
	// Element names the thing in the source document — a class, an attribute, an id
	// — so a reader can find it in the tool the document came from.
	Element string `json:"element,omitempty"`
	Message string `json:"message"`
}

// ImportResult is a parsed document: the model it became, and the honest account of
// the difference between the two.
type ImportResult struct {
	Format string       `json:"format"`
	Model  Model        `json:"model"`
	Notes  []ImportNote `json:"notes"`
}

// maxImportNotes caps the account. A 400-class model with a profile applied to every
// class would otherwise answer with a wall of identical sentences; the tail is
// summarized rather than printed.
const maxImportNotes = 200

type noteList struct {
	notes   []ImportNote
	dropped int
}

func (n *noteList) add(level, element, format string, args ...any) {
	if len(n.notes) >= maxImportNotes {
		n.dropped++
		return
	}
	n.notes = append(n.notes, ImportNote{Level: level, Element: element, Message: fmt.Sprintf(format, args...)})
}

func (n *noteList) result() []ImportNote {
	out := n.notes
	if out == nil {
		out = []ImportNote{}
	}
	if n.dropped > 0 {
		out = append(out, ImportNote{Level: NoteInfo,
			Message: fmt.Sprintf("%d further notes were not listed: the document says a great many things this build does not author.", n.dropped)})
	}
	return out
}

// DetectImportFormat reads the first meaningful byte, which is all it takes: a JSON
// document starts with an object and an XMI one with an element or a declaration.
// Sniffing rather than trusting a file extension is deliberate — a UML tool writes
// .uml, .xmi and .xml for the same document.
func DetectImportFormat(document []byte) (string, bool) {
	trimmed := bytes.TrimSpace(document)
	if len(trimmed) == 0 {
		return "", false
	}
	switch trimmed[0] {
	case '{':
		return ImportFormatJSON, true
	case '<':
		return ImportFormatXMI, true
	}
	return "", false
}

// ParseImport turns a document into a storable model and the account of what the
// import did to it. An empty format is detected from the document itself.
//
// It never returns a model Validate would refuse: everything outside the subset is
// dropped or adjusted here, with a note, rather than being handed to the store and
// refused there — a refusal names a rule, and a person importing somebody else's
// diagram needs to know which of *their* elements it was about.
func ParseImport(format string, document []byte) (ImportResult, error) {
	notes := &noteList{}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		detected, ok := DetectImportFormat(document)
		if !ok {
			return ImportResult{}, errors.New("this document is neither JSON nor XML: an import is an Atlas " +
				"information model as JSON, or a UML class diagram as XMI")
		}
		format = detected
	}

	var (
		parsed Model
		err    error
	)
	switch format {
	case ImportFormatJSON:
		parsed, err = importJSON(document)
	case ImportFormatXMI:
		parsed, err = importXMI(document, notes)
	default:
		return ImportResult{}, fmt.Errorf("Atlas does not read %q documents: an import is JSON or XMI", format)
	}
	if err != nil {
		return ImportResult{}, err
	}

	model := sanitizeImport(parsed, notes)
	if len(model.Classes) == 0 {
		return ImportResult{}, errors.New("this document contains no classes Atlas could read")
	}
	layoutImported(&model, notes)
	return ImportResult{Format: format, Model: model, Notes: notes.result()}, nil
}

// importedDocument is the JSON shape an import accepts: the content of a model, and
// nothing about the installation it came from. Ids, revisions, timestamps and the
// validation verdict a GET carries are read and discarded — they belong to the model
// they were read from, and carrying them in would let one installation's revision
// counter or ownership arrive as fact in another.
type importedDocument struct {
	Name          string            `json:"name"`
	Documentation string            `json:"documentation"`
	Classes       []Class           `json:"classes"`
	Associations  []Association     `json:"associations"`
	Stores        []DataStore       `json:"stores"`
	Model         *importedDocument `json:"model"`
}

func importJSON(document []byte) (Model, error) {
	var doc importedDocument
	if err := json.Unmarshal(document, &doc); err != nil {
		return Model{}, fmt.Errorf("read the JSON document: %w", err)
	}
	// A nested {"model": …} is what this service's own import response carries, so a
	// person who saved a preview can hand it straight back.
	if doc.Model != nil && len(doc.Classes) == 0 {
		nested := *doc.Model
		nested.Model = nil
		doc = nested
	}
	return Model{
		Name:          strings.TrimSpace(doc.Name),
		Documentation: strings.TrimSpace(doc.Documentation),
		Classes:       doc.Classes,
		Associations:  doc.Associations,
		Stores:        doc.Stores,
	}, nil
}

// sanitizeImport is where a foreign document becomes an Atlas model. Every rule
// validation.go checks is enforced here by *removing or adjusting* rather than by
// refusing, and every removal is stated.
func sanitizeImport(doc Model, notes *noteList) Model {
	out := Model{
		Name:          strings.TrimSpace(doc.Name),
		Documentation: strings.TrimSpace(doc.Documentation),
		Classes:       []Class{},
		Associations:  []Association{},
		Stores:        []DataStore{},
	}

	local := 0
	nextID := func(prefix string) string {
		local++
		return fmt.Sprintf("import-%s-%d", prefix, local)
	}

	seenID := map[string]bool{}
	seenName := map[string]bool{}
	for _, c := range doc.Classes {
		name := strings.TrimSpace(c.Name)
		label := name
		if label == "" {
			label = c.ID
		}
		if name == "" {
			notes.add(NoteDropped, c.ID, "A class with no name was dropped: a name is what a data object's itemSubjectRef refers to.")
			continue
		}
		stereotype := strings.TrimSpace(c.Stereotype)
		if stereotype == "" {
			stereotype = StereotypeBusinessObject
			notes.add(NoteInfo, name, "%s says no kind of class, so it was read as a business object.", name)
		}
		if !knownStereotype(stereotype) {
			notes.add(NoteDropped, name, "%s is a %q class, which Atlas does not author. This build has business objects, value types and enumerations.", name, c.Stereotype)
			continue
		}
		if seenName[name] {
			notes.add(NoteDropped, name, "A second class called %q was dropped: a data object naming that type could not say which it meant.", name)
			continue
		}
		seenName[name] = true

		id := strings.TrimSpace(c.ID)
		if id == "" || seenID[id] {
			id = nextID("class")
		}
		seenID[id] = true
		out.Classes = append(out.Classes, Class{
			ID: id, Name: name, Documentation: strings.TrimSpace(c.Documentation),
			Stereotype: stereotype, Attributes: c.Attributes, Literals: c.Literals,
			Identity: c.Identity, X: c.X, Y: c.Y,
		})
	}

	byName := map[string]bool{}
	for _, c := range out.Classes {
		byName[c.Name] = true
	}

	kept := out.Classes[:0]
	for i := range out.Classes {
		if sanitizeClass(&out.Classes[i], byName, notes) {
			kept = append(kept, out.Classes[i])
		}
	}
	out.Classes = kept

	byID := map[string]*Class{}
	byName = map[string]bool{}
	for i := range out.Classes {
		byID[out.Classes[i].ID] = &out.Classes[i]
		byName[out.Classes[i].Name] = true
	}

	out.Associations = sanitizeAssociations(doc.Associations, byID, nextID, notes)
	out.Stores = sanitizeStores(doc.Stores, byID, nextID, notes)
	return out
}

// sanitizeClass normalizes one class in place and reports whether it survived.
func sanitizeClass(c *Class, byName map[string]bool, notes *noteList) bool {
	kind, _ := stereotypeOf(c.Stereotype)

	if !kind.HasAttributes {
		if len(c.Attributes) > 0 {
			notes.add(NoteDropped, c.Name, "%s is an enumeration, so its %d attribute(s) were dropped: an enumeration holds literals.", c.Name, len(c.Attributes))
		}
		c.Attributes = []Attribute{}
		c.Literals = sanitizeLiterals(c, notes)
		if len(c.Literals) == 0 {
			notes.add(NoteDropped, c.Name, "%s is an enumeration with no literals, so nothing could ever be typed as it.", c.Name)
			return false
		}
	} else {
		if len(c.Literals) > 0 {
			notes.add(NoteDropped, c.Name, "%s is not an enumeration, so its %d literal(s) were dropped.", c.Name, len(c.Literals))
			c.Literals = nil
		}
		c.Attributes = sanitizeAttributes(c, byName, notes)
	}

	c.Identity = sanitizeIdentity(c, kind, notes)
	return true
}

func sanitizeLiterals(c *Class, notes *noteList) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, lit := range c.Literals {
		value := strings.TrimSpace(lit)
		if value == "" {
			notes.add(NoteDropped, c.Name, "%s has a literal with no name, which was dropped.", c.Name)
			continue
		}
		if seen[value] {
			notes.add(NoteDropped, c.Name, "%s lists the literal %q twice; the repeat was dropped.", c.Name, value)
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func sanitizeAttributes(c *Class, byName map[string]bool, notes *noteList) []Attribute {
	out := []Attribute{}
	seen := map[string]bool{}
	for _, a := range c.Attributes {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			notes.add(NoteDropped, c.Name, "%s has an attribute with no name, which was dropped.", c.Name)
			continue
		}
		if seen[name] {
			notes.add(NoteDropped, c.Name+"."+name, "%s has two attributes called %q; the second was dropped.", c.Name, name)
			continue
		}
		seen[name] = true

		attr := Attribute{Name: name, Documentation: strings.TrimSpace(a.Documentation)}
		attr.Multiplicity = normalizeMultiplicity(a.Multiplicity, c.Name+"."+name, notes)

		typeName := strings.TrimSpace(a.Type)
		switch {
		case typeName == "":
			attr.Type = TypeString
			notes.add(NoteAdjusted, c.Name+"."+name, "%s.%s states no type, so it was read as text.", c.Name, name)
		case primitiveKnown(typeName):
			attr.Type = typeName
		case typeName == c.Name:
			notes.add(NoteDropped, c.Name+"."+name, "%s.%s is typed as %s itself, which has no end. Draw an association instead.", c.Name, name, c.Name)
			continue
		case byName[typeName]:
			attr.Type = typeName
		default:
			attr.Type = TypeString
			notes.add(NoteAdjusted, c.Name+"."+name, "%s.%s is typed %q, which this document does not define, so it was read as text.", c.Name, name, typeName)
		}
		out = append(out, attr)
	}
	return out
}

func primitiveKnown(t string) bool {
	_, ok := PrimitiveOf(t)
	return ok
}

func sanitizeIdentity(c *Class, kind StereotypeKind, notes *noteList) []string {
	if len(c.Identity) == 0 {
		return nil
	}
	if !kind.HasIdentity {
		notes.add(NoteDropped, c.Name, "%s is a %s, which has nothing to identify, so its business key was dropped.", c.Name, strings.ToLower(kind.Label))
		return nil
	}
	byName := map[string]Attribute{}
	for _, a := range c.Attributes {
		byName[a.Name] = a
	}
	out := []string{}
	seen := map[string]bool{}
	for _, key := range c.Identity {
		name := strings.TrimSpace(key)
		if name == "" || seen[name] {
			continue
		}
		attr, ok := byName[name]
		if !ok {
			notes.add(NoteDropped, c.Name, "%s's business key names %q, which is not one of its attributes, so that part of the key was dropped.", c.Name, name)
			continue
		}
		// The multiplicity is one of the four by now: sanitizeAttributes normalized it
		// before this ran, so there is no unknown case left to skip.
		mult, _ := MultiplicityOf(attr.Multiplicity)
		if !mult.Required {
			notes.add(NoteDropped, c.Name+"."+name, "%s.%s is part of the business key but may be absent, so it was dropped from the key: a key that can be missing identifies nothing.", c.Name, name)
			continue
		}
		if mult.Collection {
			notes.add(NoteDropped, c.Name+"."+name, "%s.%s is part of the business key but holds a list, so it was dropped from the key: a key has to be one value.", c.Name, name)
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeAssociations(in []Association, byID map[string]*Class, nextID func(string) string, notes *noteList) []Association {
	out := []Association{}
	seen := map[string]bool{}
	// parents accumulates the generalizations already accepted, so an edge that would
	// close a specialization chain on itself can be refused as it arrives. A cycle is
	// wrong as a pair rather than at either link, so the last edge in is the one that
	// goes: it is the one the document added to an otherwise sound hierarchy.
	parents := map[string][]string{}

	for _, a := range in {
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			kind = KindAssociation
		}
		from, okFrom := byID[strings.TrimSpace(a.From.ClassID)]
		to, okTo := byID[strings.TrimSpace(a.To.ClassID)]
		label := associationLabel(a, from, to)
		if !knownKind(kind) {
			notes.add(NoteDropped, label, "%s is a %q relationship, which Atlas does not author. This build draws associations, aggregations, compositions and generalizations.", label, a.Kind)
			continue
		}
		if !okFrom || !okTo {
			missing := a.From.ClassID
			if okFrom {
				missing = a.To.ClassID
			}
			notes.add(NoteDropped, label, "A %s relationship names the class %q, which this document does not contain (or which was itself dropped), so the line was dropped.", kind, missing)
			continue
		}
		if kind == KindGeneralization && from.ID == to.ID {
			notes.add(NoteDropped, label, "%s is drawn as a kind of itself, so the line was dropped.", from.Name)
			continue
		}
		if ok, refusal := AllowAssociation(kind, from.Stereotype, to.Stereotype); !ok {
			notes.add(NoteDropped, label, "%s → %s: %s The line was dropped.", from.Name, to.Name, refusal.Message)
			continue
		}
		if kind == KindGeneralization && reaches(parents, to.ID, from.ID) {
			notes.add(NoteDropped, label, "%s is a kind of %s, which is already a kind of %s. A specialization chain cannot close on itself, so the line was dropped.", from.Name, to.Name, from.Name)
			continue
		}

		id := strings.TrimSpace(a.ID)
		if id == "" || seen[id] {
			id = nextID("association")
		}
		seen[id] = true
		next := Association{
			ID: id, Name: strings.TrimSpace(a.Name), Kind: kind,
			From: End{ClassID: from.ID, Role: strings.TrimSpace(a.From.Role)},
			To:   End{ClassID: to.ID, Role: strings.TrimSpace(a.To.Role)},
		}
		next.From.Multiplicity = normalizeEndMultiplicity(a.From.Multiplicity, label, notes)
		next.To.Multiplicity = normalizeEndMultiplicity(a.To.Multiplicity, label, notes)
		if kind == KindGeneralization {
			parents[from.ID] = append(parents[from.ID], to.ID)
		}
		out = append(out, next)
	}
	return out
}

func associationLabel(a Association, from, to *Class) string {
	name := strings.TrimSpace(a.Name)
	if name != "" {
		return name
	}
	if from != nil && to != nil {
		return from.Name + " → " + to.Name
	}
	return strings.TrimSpace(a.ID)
}

// reaches reports whether start can walk up the accepted generalizations to target.
func reaches(parents map[string][]string, start, target string) bool {
	seen := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == target {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, parents[id]...)
	}
	return false
}

func sanitizeStores(in []DataStore, byID map[string]*Class, nextID func(string) string, notes *noteList) []DataStore {
	byName := map[string]*Class{}
	for _, c := range byID {
		byName[c.Name] = c
	}
	out := []DataStore{}
	seenID := map[string]bool{}
	seenName := map[string]bool{}
	for _, st := range in {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			notes.add(NoteDropped, st.ID, "A data store with no name was dropped: a name is what a process's <dataStore> refers to it by.")
			continue
		}
		if seenName[name] {
			notes.add(NoteDropped, name, "A second data store called %q was dropped.", name)
			continue
		}
		mode := strings.TrimSpace(st.Mode)
		if mode == "" {
			mode = StoreModeRead
		}
		if _, ok := StoreModeOf(mode); !ok {
			notes.add(NoteDropped, name, "%s is a %q store, which Atlas does not author: this build reads from a store, and writing through one is a transaction against something outside the engine.", name, st.Mode)
			continue
		}
		class, ok := byName[strings.TrimSpace(st.Class)]
		if !ok {
			notes.add(NoteDropped, name, "%s holds %q, and this document has no class of that name (or it was itself dropped), so the store was dropped.", name, st.Class)
			continue
		}
		if class.Stereotype != StereotypeBusinessObject {
			notes.add(NoteDropped, name, "%s holds %s, which is not a business object. Only a business object outlives the process that made it, so the store was dropped.", name, class.Name)
			continue
		}
		if len(class.Identity) == 0 {
			notes.add(NoteDropped, name, "%s holds %s, which declares no business key, so the store was dropped: a process reads from a store by naming which thing it wants.", name, class.Name)
			continue
		}
		seenName[name] = true
		id := strings.TrimSpace(st.ID)
		if id == "" || seenID[id] {
			id = nextID("store")
		}
		seenID[id] = true
		out = append(out, DataStore{
			ID: id, Name: name, Documentation: strings.TrimSpace(st.Documentation),
			Class: class.Name, Worker: strings.TrimSpace(st.Worker), Mode: mode, X: st.X, Y: st.Y,
		})
	}
	return out
}

// normalizeMultiplicity maps whatever the document said onto the four the subset has,
// stating the difference. An unstated one is read as optional rather than refused: a
// tool that omits it is not asserting that the member is required.
func normalizeMultiplicity(raw, element string, notes *noteList) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return MultOptional
	}
	if _, ok := MultiplicityOf(value); ok {
		return value
	}
	mapped, ok := nearestMultiplicity(value)
	if !ok {
		notes.add(NoteAdjusted, element, "%s has the multiplicity %q, which Atlas cannot read, so it was read as optional.", element, raw)
		return MultOptional
	}
	notes.add(NoteAdjusted, element, "%s is %q; Atlas authors 0..1, 1, 0..* and 1..*, so it was read as %s.", element, raw, mapped)
	return mapped
}

// normalizeEndMultiplicity is the same for an association end, where saying nothing
// is allowed and means nothing was said.
func normalizeEndMultiplicity(raw, element string, notes *noteList) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if _, ok := MultiplicityOf(value); ok {
		return value
	}
	mapped, ok := nearestMultiplicity(value)
	if !ok {
		notes.add(NoteAdjusted, element, "An end of %s has the multiplicity %q, which Atlas cannot read, so it was left unsaid.", element, raw)
		return ""
	}
	notes.add(NoteAdjusted, element, "An end of %s is %q; Atlas authors 0..1, 1, 0..* and 1..*, so it was read as %s.", element, raw, mapped)
	return mapped
}

// nearestMultiplicity reads a UML multiplicity string and answers with the subset
// member that loses the least: whether a value is required, and whether there can be
// more than one of it, are the two facts the subset keeps.
func nearestMultiplicity(raw string) (string, bool) {
	lower, upper, ok := parseBounds(raw)
	if !ok {
		return "", false
	}
	return boundsToMultiplicity(lower, upper), true
}

func parseBounds(raw string) (lower int, upper int, ok bool) {
	value := strings.TrimSpace(raw)
	lowerText, upperText := value, value
	if idx := strings.Index(value, ".."); idx >= 0 {
		lowerText, upperText = strings.TrimSpace(value[:idx]), strings.TrimSpace(value[idx+2:])
	}
	lower, ok = parseBound(lowerText)
	if !ok {
		return 0, 0, false
	}
	upper, ok = parseBound(upperText)
	if !ok {
		return 0, 0, false
	}
	if lower == unbounded {
		lower = 0
	}
	return lower, upper, true
}

// unbounded is UML's `*` — an unlimited upper bound.
const unbounded = -1

func parseBound(text string) (int, bool) {
	switch strings.TrimSpace(text) {
	case "*", "-1", "n":
		return unbounded, true
	case "":
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func boundsToMultiplicity(lower, upper int) string {
	collection := upper == unbounded || upper > 1
	required := lower >= 1
	switch {
	case required && collection:
		return MultAtLeast1
	case required:
		return MultOne
	case collection:
		return MultMany
	default:
		return MultOptional
	}
}

// layoutImported arranges a model that arrived without a diagram.
//
// XMI carries the model and leaves the picture in a tool-specific file beside it, so
// an imported model routinely has every class at the origin — which draws as one
// stack of boxes and reads as nothing at all. A grid is not the layout the author
// drew, and saying so is the point of the note: what was lost is the arrangement,
// not the model.
func layoutImported(m *Model, notes *noteList) {
	for _, c := range m.Classes {
		if c.X != 0 || c.Y != 0 {
			return // the document placed its classes; leave them where the author put them
		}
	}
	const (
		originX = 60.0
		originY = 60.0
		pitchX  = 280.0
		pitchY  = 220.0
		// Four to a row. A square arrangement would be prettier for some counts and
		// this is not the arrangement the author drew either way — what matters is that
		// no two boxes sit on top of each other and the note says a person should move
		// them.
		columns = 4
	)
	for i := range m.Classes {
		m.Classes[i].X = originX + float64(i%columns)*pitchX
		m.Classes[i].Y = originY + float64(i/columns)*pitchY
	}
	rows := (len(m.Classes) + columns - 1) / columns
	for i := range m.Stores {
		if m.Stores[i].X != 0 || m.Stores[i].Y != 0 {
			continue
		}
		m.Stores[i].X = originX + float64(i%columns)*pitchX
		m.Stores[i].Y = originY + float64(rows)*pitchY
	}
	notes.add(NoteInfo, "", "The document carries no diagram geometry — XMI keeps the picture in a file of its own — so the %d classes were laid out on a grid. Move them where they belong.", len(m.Classes))
}

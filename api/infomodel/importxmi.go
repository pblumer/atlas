package infomodel

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// Reading XMI 2.5.1 — what a UML tool hands over.
//
// XMI is a serialization of the whole UML metamodel, and Atlas authors a small,
// declared subset of one diagram in it. So this reader is deliberately shaped as a
// funnel with a receipt: it looks for the handful of metaclasses the subset has a
// place for, and it says out loud what it walked past.
//
// It parses generically — a node tree, then a walk — rather than by binding structs
// to one tool's output. Every tool writes valid XMI differently: the type of an
// element is `xmi:type` in Papyrus and `xsi:type` elsewhere, a member end is an
// attribute list in one document and child elements in another, a package nests in
// one and is flat in the next. A generic walk that names what it recognizes survives
// that variance; a struct binding does not.

type xmlNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []xmlNode  `xml:",any"`
	// Text is the node's own character data — what a <body> inside an ownedComment
	// carries, which is where every UML tool puts an element's documentation.
	Text string `xml:",chardata"`
}

func (n xmlNode) local() string { return n.XMLName.Local }

// attr reads an attribute the document wrote without a prefix — `name`, `type`,
// `association`. The distinction from xmiAttr matters more than it looks: a UML
// Property carries both `xmi:type="uml:Property"` and `type="_string"`, and reading
// them by local name alone would answer one with the other.
func (n xmlNode) attr(local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && !isXMINamespace(a.Name.Space) {
			return a.Value
		}
	}
	return ""
}

// xmiAttr reads an attribute in the XMI or XML-Schema-instance namespace: xmi:id,
// xmi:type, xmi:idref, xsi:type.
func (n xmlNode) xmiAttr(local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && isXMINamespace(a.Name.Space) {
			return a.Value
		}
	}
	return ""
}

func (n xmlNode) children(local string) []xmlNode {
	out := []xmlNode{}
	for _, child := range n.Nodes {
		if child.local() == local {
			out = append(out, child)
		}
	}
	return out
}

func (n xmlNode) child(local string) (xmlNode, bool) {
	for _, child := range n.Nodes {
		if child.local() == local {
			return child, true
		}
	}
	return xmlNode{}, false
}

func isXMINamespace(space string) bool {
	upper := strings.ToUpper(space)
	return strings.Contains(upper, "XMI") || strings.Contains(upper, "XMLSCHEMA-INSTANCE")
}

// elementID is the identity a document gives a node: xmi:id in XMI 2.x, xmi.id in
// 1.x, a bare id where a tool wrote one.
func elementID(n xmlNode) string {
	for _, candidate := range []string{n.xmiAttr("id"), n.attr("id"), n.xmiAttr("uuid")} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

// elementKind is the metaclass a node is an instance of: `uml:Class` reads as Class.
// A document that states no type falls back to the element's own name, which is how
// XMI 1.x wrote it (`<UML:Class>`).
func elementKind(n xmlNode) string {
	kind := n.xmiAttr("type")
	if kind == "" {
		// XMI 1.x states the metaclass in the element itself: <UML:Class …>. Reading a
		// plain `type` attribute here instead would be wrong — on a Property that
		// attribute is the reference to the property's *type*, not its metaclass.
		kind = n.local()
	}
	if idx := strings.LastIndex(kind, ":"); idx >= 0 {
		kind = kind[idx+1:]
	}
	return kind
}

func idRef(n xmlNode) string {
	for _, candidate := range []string{n.xmiAttr("idref"), n.attr("idref")} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	if href := strings.TrimSpace(n.attr("href")); href != "" {
		if idx := strings.LastIndex(href, "#"); idx >= 0 {
			return href[idx+1:]
		}
		return href
	}
	return ""
}

// The metaclasses that carry the model. Everything else in a document is either tool
// metadata (skipped without comment, because it is not a statement about the
// business) or a modeling element outside the subset (dropped with a note).
const (
	kindPackage       = "Package"
	kindModel         = "Model"
	kindClass         = "Class"
	kindDataType      = "DataType"
	kindEnumeration   = "Enumeration"
	kindPrimitiveType = "PrimitiveType"
	kindAssociation   = "Association"
)

// metadataElements are the parts of a document that describe the file rather than
// the business: profile applications, tool extensions, imports. They are not
// modeling elements, so walking past one is not a loss worth reporting.
var metadataElements = map[string]bool{
	"Extension": true, "Documentation": true, "profileApplication": true,
	"elementImport": true, "packageImport": true, "importedPackage": true,
	"appliedProfile": true, "eAnnotations": true, "ownedComment": true,
}

type xmiElement struct {
	id      string
	kind    string
	name    string
	node    xmlNode
	pkgName string
}

type xmiProperty struct {
	id           string
	name         string
	typeRef      string
	aggregation  string
	multiplicity string
	// owner is the class that owns this property, empty when the association owns it.
	// It is what says which end of a binary association a class points at.
	owner string
	isID  bool
	// boundsStated is whether the document said anything about the multiplicity at
	// all, which is what lets an identifier with no bounds be read as required.
	boundsStated bool
}

type xmiDocument struct {
	elements   []xmiElement
	byID       map[string]xmiElement
	properties map[string]xmiProperty
	// stereotypes maps a class id to an applied stereotype's name — the standard
	// encoding for «valueType» is an element of its own pointing back at the class.
	stereotypes map[string]string
	name        string
	docs        string
}

func importXMI(document []byte, notes *noteList) (Model, error) {
	var root xmlNode
	if err := xml.Unmarshal(document, &root); err != nil {
		return Model{}, fmt.Errorf("read the XMI document: %w", err)
	}
	doc := &xmiDocument{
		byID: map[string]xmiElement{}, properties: map[string]xmiProperty{},
		stereotypes: map[string]string{},
	}
	doc.readRoot(root, notes)
	doc.readStereotypes(root)
	return doc.model(notes), nil
}

func (d *xmiDocument) readRoot(root xmlNode, notes *noteList) {
	// A document is rooted either at the model itself (`<uml:Model>`) or at an XMI
	// envelope holding one or more.
	if kind := elementKind(root); kind == kindModel || kind == kindPackage ||
		root.local() == kindModel || root.local() == kindPackage {
		d.name = strings.TrimSpace(root.attr("name"))
		d.docs = commentOf(root)
		d.collect(root, "", notes)
		return
	}
	for _, child := range root.Nodes {
		kind := elementKind(child)
		if kind == kindModel || kind == kindPackage || child.local() == kindModel || child.local() == kindPackage {
			if d.name == "" {
				d.name = strings.TrimSpace(child.attr("name"))
				d.docs = commentOf(child)
			}
			d.collect(child, "", notes)
		}
	}
	if len(d.elements) == 0 {
		d.collect(root, "", notes)
	}
}

// collect walks one package's contents, flattening nested packages. A class's
// identity in this model is its name, and a name is unique per model rather than per
// package, so nesting cannot survive; a package that held classes says so once.
func (d *xmiDocument) collect(container xmlNode, pkgName string, notes *noteList) {
	for _, child := range container.Nodes {
		switch child.local() {
		case "packagedElement", "ownedMember", "ownedElement", "nestedClassifier":
		default:
			continue
		}
		kind := elementKind(child)
		if kind == child.local() {
			// The element states no metaclass of its own, so it is named by the slot it
			// sits in. Every dialect that omits the type means a class.
			kind = kindClass
		}
		name := strings.TrimSpace(child.attr("name"))
		if kind == kindPackage || kind == kindModel {
			if name != "" {
				notes.add(NoteInfo, name, "The package %q was flattened: a class is named once per information model, not once per package.", name)
			}
			d.collect(child, name, notes)
			continue
		}
		element := xmiElement{id: elementID(child), kind: kind, name: name, node: child, pkgName: pkgName}
		d.elements = append(d.elements, element)
		if element.id != "" {
			d.byID[element.id] = element
		}
	}
}

// readStereotypes finds applied stereotypes. UML writes one as an element of its own
// — `<Atlas:valueType base_Class="_address"/>` — anywhere in the document, so this is
// a walk of the whole tree rather than of the packaged elements.
func (d *xmiDocument) readStereotypes(n xmlNode) {
	if base := strings.TrimSpace(n.attr("base_Class")); base != "" {
		d.stereotypes[base] = n.local()
	}
	for _, child := range n.Nodes {
		d.readStereotypes(child)
	}
}

func commentOf(n xmlNode) string {
	comment, ok := n.child("ownedComment")
	if !ok {
		return ""
	}
	if body, ok := comment.child("body"); ok {
		return strings.TrimSpace(body.Text)
	}
	return strings.TrimSpace(comment.attr("body"))
}

// model turns the collected elements into an Atlas model. Everything it cannot place
// is reported here, where the element still has its own name to be reported under.
func (d *xmiDocument) model(notes *noteList) Model {
	out := Model{Name: d.name, Documentation: d.docs}
	classIDs := map[string]bool{}
	for _, e := range d.elements {
		if stereotypeForKind(e.kind, d.stereotypes[e.id]) != "" {
			classIDs[e.id] = true
		}
	}

	generalizations := []Association{}
	for _, e := range d.elements {
		switch e.kind {
		case kindPrimitiveType:
			// A primitive is a type to resolve against, not a class of its own.
			continue
		case kindAssociation:
			d.readAssociationEnds(e)
			continue
		}
		stereotype := stereotypeForKind(e.kind, d.stereotypes[e.id])
		if stereotype == "" {
			if metadataElements[e.kind] {
				continue
			}
			label := e.name
			if label == "" {
				label = e.id
			}
			notes.add(NoteDropped, label, "%s is a uml:%s, which Atlas does not author: an information model has business objects, value types and enumerations.", label, e.kind)
			continue
		}
		class, gens := d.readClass(e, stereotype, classIDs, notes)
		out.Classes = append(out.Classes, class)
		generalizations = append(generalizations, gens...)
	}

	for _, e := range d.elements {
		if e.kind != kindAssociation {
			continue
		}
		if a, ok := d.readAssociation(e, notes); ok {
			out.Associations = append(out.Associations, a)
		}
	}
	out.Associations = append(out.Associations, generalizations...)
	return out
}

// stereotypeForKind maps a UML metaclass — and any stereotype applied to it — onto
// one of the three kinds this build authors. An empty answer means the element is
// not a class at all.
func stereotypeForKind(kind, applied string) string {
	switch strings.ToLower(strings.TrimPrefix(applied, "uml:")) {
	case "businessobject":
		return StereotypeBusinessObject
	case "valuetype":
		return StereotypeValueType
	case "enumeration":
		return StereotypeEnumeration
	}
	switch kind {
	case kindClass:
		return StereotypeBusinessObject
	case kindDataType, "Struct", "StructuredDataType":
		// A data type is a value: two of them with equal contents are the same thing,
		// which is exactly what a value type says.
		return StereotypeValueType
	case kindEnumeration:
		return StereotypeEnumeration
	}
	return ""
}

func (d *xmiDocument) readClass(e xmiElement, stereotype string, classIDs map[string]bool, notes *noteList) (Class, []Association) {
	// A class the document did not name keeps its empty name: the sanitizer drops it
	// and says so, which is a truer answer than a class called EAID_A1B2C3.
	name := e.name
	if name == "" {
		name = e.id
	}
	class := Class{ID: e.id, Name: e.name, Stereotype: stereotype, Documentation: commentOf(e.node)}
	if strings.EqualFold(e.node.attr("isAbstract"), "true") {
		notes.add(NoteInfo, name, "%s is abstract. Atlas does not author abstract classes, so it was imported as an ordinary one; nothing stops a process from naming it.", name)
	}
	for _, op := range e.node.children("ownedOperation") {
		opName := strings.TrimSpace(op.attr("name"))
		if opName == "" {
			opName = "an operation"
		}
		notes.add(NoteDropped, name+"."+opName, "%s.%s is an operation, which is out of subset: an information model describes the records a process moves, and behaviour belongs to the BPMN model beside it.", name, opName)
	}
	for _, lit := range e.node.children("ownedLiteral") {
		class.Literals = append(class.Literals, strings.TrimSpace(lit.attr("name")))
	}

	for _, attr := range e.node.children("ownedAttribute") {
		property := d.readProperty(attr, e.id, name, notes)
		if property.id != "" {
			d.properties[property.id] = property
		}
		if strings.TrimSpace(attr.attr("association")) != "" {
			continue // this is one end of an association; the association states it
		}
		if property.name == "" {
			notes.add(NoteDropped, name, "%s has an attribute with no name, which was dropped.", name)
			continue
		}
		multiplicity := property.multiplicity
		if property.isID && !property.boundsStated {
			// The document does say something about this member: it identifies the
			// instance. An identifier that may be absent identifies nothing, so an
			// unstated multiplicity on one is read as required rather than dropping
			// the business key — the single most valuable fact in the document.
			multiplicity = MultOne
			notes.add(NoteInfo, name+"."+property.name,
				"%s.%s identifies the class and states no multiplicity, so it was read as required: a business key that can be absent identifies nothing.", name, property.name)
		}
		class.Attributes = append(class.Attributes, Attribute{
			Name: property.name, Type: d.typeNameOf(property.typeRef, classIDs),
			Multiplicity: multiplicity, Documentation: commentOf(attr),
		})
		if property.isID {
			class.Identity = append(class.Identity, property.name)
		}
	}

	generalizations := []Association{}
	for _, gen := range e.node.children("generalization") {
		general := strings.TrimSpace(gen.attr("general"))
		if general == "" {
			if child, ok := gen.child("general"); ok {
				general = idRef(child)
			}
		}
		if general == "" {
			notes.add(NoteDropped, name, "%s has a generalization that names no general class, so it was dropped.", name)
			continue
		}
		generalizations = append(generalizations, Association{
			ID: elementID(gen), Kind: KindGeneralization,
			From: End{ClassID: e.id}, To: End{ClassID: general},
		})
	}
	return class, generalizations
}

// readProperty reads one Property — an attribute, or one end of an association.
func (d *xmiDocument) readProperty(n xmlNode, owner, ownerName string, notes *noteList) xmiProperty {
	name := strings.TrimSpace(n.attr("name"))
	typeRef := strings.TrimSpace(n.attr("type"))
	if typeRef == "" {
		if child, ok := n.child("type"); ok {
			typeRef = idRef(child)
		}
	}
	label := name
	if ownerName != "" && name != "" {
		label = ownerName + "." + name
	} else if label == "" {
		label = ownerName
	}
	multiplicity, stated := multiplicityOf(n, label, notes)
	return xmiProperty{
		id: elementID(n), name: name, typeRef: typeRef,
		aggregation:  strings.TrimSpace(n.attr("aggregation")),
		multiplicity: multiplicity,
		owner:        owner,
		isID:         strings.EqualFold(n.attr("isID"), "true"),
		boundsStated: stated,
	}
}

// multiplicityOf reads a property's bounds. UML writes them as child elements
// (`<lowerValue value="0"/>`) and, in some dialects, as attributes; an unstated bound
// is 1, which is the specification's own default.
func multiplicityOf(n xmlNode, label string, notes *noteList) (string, bool) {
	lower, hasLower := boundOf(n, "lower", "lowerValue", 1)
	upper, hasUpper := boundOf(n, "upper", "upperValue", 1)
	if !hasLower && !hasUpper {
		// Nothing was said at all. UML's default is 1..1, but a tool that omits the
		// bounds usually has not thought about them, so reading it as required would
		// invent a rule the document does not state.
		return MultOptional, false
	}
	mapped := boundsToMultiplicity(lower, upper)
	if lower > 1 || upper > 1 {
		notes.add(NoteAdjusted, label, "%s is %s; Atlas authors 0..1, 1, 0..* and 1..*, so it was read as %s. What it keeps is whether a value is required and whether there can be more than one.",
			label, boundsText(lower, upper), mapped)
	}
	return mapped, true
}

func boundOf(n xmlNode, attrName, childName string, missing int) (int, bool) {
	if raw := strings.TrimSpace(n.attr(attrName)); raw != "" {
		if value, ok := parseBound(raw); ok {
			return value, true
		}
	}
	child, ok := n.child(childName)
	if !ok {
		return missing, false
	}
	raw := strings.TrimSpace(child.attr("value"))
	if raw == "" {
		// A literal with no value: an integer literal is 0, and an unlimited natural
		// one is written with its value, so the sane reading of the empty case is the
		// literal's own default.
		if strings.Contains(strings.ToLower(elementKind(child)), "unlimited") {
			return 1, true
		}
		return 0, true
	}
	value, ok := parseBound(raw)
	if !ok {
		return missing, false
	}
	return value, true
}

func boundsText(lower, upper int) string {
	upperText := "*"
	if upper != unbounded {
		upperText = strconv.Itoa(upper)
	}
	if lower == upper {
		return upperText
	}
	return strconv.Itoa(lower) + ".." + upperText
}

// typeNameOf resolves a type reference to the name an Atlas attribute states. A
// reference to a class in this document is that class; anything else is read as the
// primitive its name means. A name that means nothing here is returned unchanged, so
// the sanitizer reports it against the attribute that used it.
func (d *xmiDocument) typeNameOf(ref string, classIDs map[string]bool) string {
	if ref == "" {
		return ""
	}
	name := ref
	if element, ok := d.byID[ref]; ok {
		if classIDs[ref] {
			if element.name != "" {
				return element.name
			}
			return element.id
		}
		if element.name != "" {
			name = element.name
		}
	}
	if primitive, ok := umlPrimitives[strings.ToLower(name)]; ok {
		return primitive
	}
	return name
}

// umlPrimitives maps the type names UML tools use onto the seven this build has. The
// mapping is deliberately coarse: a process information model says a delivery date is
// a date, not that it is a DATETIME2(7), so every width and precision a tool states
// collapses here on purpose.
var umlPrimitives = map[string]string{
	"string": TypeString, "str": TypeString, "text": TypeString, "char": TypeString,
	"character": TypeString, "varchar": TypeString, "nvarchar": TypeString, "uuid": TypeString,
	"integer": TypeNumber, "int": TypeNumber, "long": TypeNumber, "short": TypeNumber,
	"byte": TypeNumber, "real": TypeNumber, "double": TypeNumber, "float": TypeNumber,
	"decimal": TypeNumber, "number": TypeNumber, "numeric": TypeNumber, "money": TypeNumber,
	"currency": TypeNumber, "unlimitednatural": TypeNumber,
	"boolean": TypeBoolean, "bool": TypeBoolean,
	"date":     TypeDate,
	"datetime": TypeDateTime, "timestamp": TypeDateTime, "instant": TypeDateTime,
	"time":     TypeTime,
	"duration": TypeDuration,
}

// readAssociationEnds registers the ends an association owns, so they can be resolved
// whichever order the document lists its elements in.
func (d *xmiDocument) readAssociationEnds(e xmiElement) {
	for _, end := range e.node.children("ownedEnd") {
		property := d.readProperty(end, "", associationName(e), &noteList{})
		if property.id != "" {
			d.properties[property.id] = property
		}
	}
}

func associationName(e xmiElement) string {
	if e.name != "" {
		return e.name
	}
	return "the association"
}

func (d *xmiDocument) readAssociation(e xmiElement, notes *noteList) (Association, bool) {
	label := associationName(e)
	ids := strings.Fields(e.node.attr("memberEnd"))
	for _, member := range e.node.children("memberEnd") {
		if ref := idRef(member); ref != "" {
			ids = append(ids, ref)
		}
	}
	if len(ids) == 0 {
		for _, end := range e.node.children("ownedEnd") {
			if id := elementID(end); id != "" {
				ids = append(ids, id)
			}
		}
	}
	// Re-read the ends this association owns, now with a notes list that reports.
	for _, end := range e.node.children("ownedEnd") {
		property := d.readProperty(end, "", label, notes)
		if property.id != "" {
			d.properties[property.id] = property
		}
	}

	if len(ids) != 2 {
		notes.add(NoteDropped, label, "%s has %d ends. Every relationship Atlas authors has exactly two; an n-ary association is modeled as a class between the participants.", label, len(ids))
		return Association{}, false
	}
	ends := make([]xmiProperty, 0, 2)
	for _, id := range ids {
		property, ok := d.properties[id]
		if !ok {
			notes.add(NoteDropped, label, "%s names an end (%s) the document does not define, so the line was dropped.", label, id)
			return Association{}, false
		}
		ends = append(ends, property)
	}

	kind := KindAssociation
	whole := -1
	for i, end := range ends {
		switch strings.ToLower(end.aggregation) {
		case "composite":
			kind, whole = KindComposition, i
		case "shared":
			kind, whole = KindAggregation, i
		}
	}

	from, to := ends[0], ends[1]
	switch {
	case whole >= 0:
		// The composite end is the whole — that is what the diamond marks — so the
		// relationship runs from it to the part.
		from, to = ends[whole], ends[1-whole]
	case ends[0].owner != "" && ends[1].owner == "":
		// An end a class owns is the end that class points *at*, so its owner is the
		// other side of the line.
		from, to = ends[1], ends[0]
	case ends[1].owner != "" && ends[0].owner == "":
		from, to = ends[0], ends[1]
	}

	return Association{
		ID: e.id, Name: e.name, Kind: kind,
		From: End{ClassID: from.typeRef, Role: from.name, Multiplicity: from.multiplicity},
		To:   End{ClassID: to.typeRef, Role: to.name, Multiplicity: to.multiplicity},
	}, true
}

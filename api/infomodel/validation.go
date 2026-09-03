package infomodel

import (
	"fmt"
	"sort"
	"strings"
)

// Validation is what turns a drawing into a model.
//
// The checks fall into two kinds, and the Reason on a finding keeps them apart
// because a modeler acts on them differently. A finding whose reason is
// RefusedOutOfSubset says *this build does not author that* — UML would allow it,
// and the answer may one day be to widen the subset. A finding whose reason is
// RefusedByNotation says *that is not a thing* — an enumeration at the end of a
// relationship, a value type owning parts, a class that is a kind of itself. Only
// the second is a modeling mistake.
//
// Validation runs on write, and its findings are what the canvas marks. It is
// deliberately a whole-model check rather than a per-edit one: a generalization
// cycle is not wrong at either of its links, only as a pair.

// The finding codes. They are a public contract — the canvas keys its messages and
// its marks off them — so a code is added rather than repurposed.
const (
	CodeMissingClassName         = "missing-class-name"
	CodeDuplicateClassName       = "duplicate-class-name"
	CodeDuplicateClassID         = "duplicate-class-id"
	CodeUnknownStereotype        = "unknown-stereotype"
	CodeMissingAttributeName     = "missing-attribute-name"
	CodeDuplicateAttribute       = "duplicate-attribute"
	CodeUnknownType              = "unknown-type"
	CodeUnknownMultiplicity      = "unknown-multiplicity"
	CodeAttributeTypedAsSelf     = "attribute-typed-as-self"
	CodeEnumerationHasAttributes = "enumeration-has-attributes"
	CodeEnumerationEmpty         = "enumeration-empty"
	CodeDuplicateLiteral         = "duplicate-literal"
	CodeIdentityNotAllowed       = "identity-not-allowed"
	CodeUnknownIdentityAttribute = "unknown-identity-attribute"
	CodeIdentityNotRequired      = "identity-not-required"
	CodeIdentityNotSingular      = "identity-not-singular"
	CodeUnknownClassRef          = "unknown-class-ref"
	CodeRelationshipRefused      = "relationship-refused"
	CodeSelfGeneralization       = "self-generalization"
	CodeGeneralizationCycle      = "generalization-cycle"
	CodeDuplicateAssociationID   = "duplicate-association-id"
	CodeStoreMissingName         = "store-missing-name"
	CodeDuplicateStoreName       = "duplicate-store-name"
	CodeDuplicateStoreID         = "duplicate-store-id"
	CodeStoreUnknownClass        = "store-unknown-class"
	CodeStoreClassNotStorable    = "store-class-not-storable"
	CodeStoreClassHasNoKey       = "store-class-has-no-key"
	CodeStoreUnknownMode         = "store-unknown-mode"
)

// Finding is one thing wrong with a model, located precisely enough that the
// canvas can mark it rather than print a list somebody has to search.
type Finding struct {
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	// Exactly one of ClassID / AssociationID / StoreID identifies where the finding
	// is; Attribute names the member within a class when the finding is about one.
	ClassID       string `json:"classId,omitempty"`
	AssociationID string `json:"associationId,omitempty"`
	StoreID       string `json:"storeId,omitempty"`
	Attribute     string `json:"attribute,omitempty"`
}

// ValidationResult is the whole verdict. Findings are ordered by where they are, so
// a re-validation of an unchanged model produces an identical list.
type ValidationResult struct {
	Valid    bool      `json:"valid"`
	Findings []Finding `json:"findings"`
}

// Validate checks a whole model against the subset and against itself.
func Validate(m Model) ValidationResult {
	findings := []Finding{}
	add := func(f Finding) { findings = append(findings, f) }

	classByID := map[string]*Class{}
	classByName := map[string]*Class{}
	seenID := map[string]bool{}
	for i := range m.Classes {
		c := &m.Classes[i]
		if seenID[c.ID] {
			add(Finding{Code: CodeDuplicateClassID, Reason: RefusedByNotation, ClassID: c.ID,
				Message: fmt.Sprintf("Two classes share the id %q. An association names its ends by id, so it could not say which one it meant.", c.ID)})
			continue
		}
		seenID[c.ID] = true
		classByID[c.ID] = c
	}
	for i := range m.Classes {
		c := &m.Classes[i]
		name := strings.TrimSpace(c.Name)
		if name == "" {
			add(Finding{Code: CodeMissingClassName, Reason: RefusedByNotation, ClassID: c.ID,
				Message: "A class needs a name: it is what a BPMN data object's itemSubjectRef refers to."})
			continue
		}
		if prev, dup := classByName[name]; dup {
			add(Finding{Code: CodeDuplicateClassName, Reason: RefusedByNotation, ClassID: c.ID,
				Message: fmt.Sprintf("Two classes are called %q (%s and %s). A data object that names that type could not say which it meant.", name, prev.ID, c.ID)})
			continue
		}
		classByName[name] = c
	}

	for i := range m.Classes {
		validateClass(&m.Classes[i], classByName, add)
	}
	validateAssociations(m, classByID, add)
	validateStores(m, classByName, add)

	sort.SliceStable(findings, func(a, b int) bool {
		x, y := findings[a], findings[b]
		if x.ClassID != y.ClassID {
			return x.ClassID < y.ClassID
		}
		if x.AssociationID != y.AssociationID {
			return x.AssociationID < y.AssociationID
		}
		if x.StoreID != y.StoreID {
			return x.StoreID < y.StoreID
		}
		if x.Attribute != y.Attribute {
			return x.Attribute < y.Attribute
		}
		return x.Code < y.Code
	})
	return ValidationResult{Valid: len(findings) == 0, Findings: findings}
}

func validateClass(c *Class, classByName map[string]*Class, add func(Finding)) {
	kind, known := stereotypeOf(c.Stereotype)
	if !known {
		add(Finding{Code: CodeUnknownStereotype, Reason: RefusedOutOfSubset, ClassID: c.ID,
			Message: fmt.Sprintf("Atlas does not author %q classes. This build has business objects, value types and enumerations.", c.Stereotype)})
		return // every rule below is about which of the three this is
	}

	if kind.HasAttributes {
		validateAttributes(c, classByName, add)
	} else if len(c.Attributes) > 0 {
		add(Finding{Code: CodeEnumerationHasAttributes, Reason: RefusedByNotation, ClassID: c.ID,
			Message: fmt.Sprintf("%s is an enumeration, so it holds literals rather than attributes. Make it a value type if its members need fields of their own.", c.Name)})
	}

	if c.Stereotype == StereotypeEnumeration {
		if len(c.Literals) == 0 {
			add(Finding{Code: CodeEnumerationEmpty, Reason: RefusedByNotation, ClassID: c.ID,
				Message: fmt.Sprintf("%s is an enumeration with no literals, so nothing could ever be typed as it.", c.Name)})
		}
		seen := map[string]bool{}
		for _, lit := range c.Literals {
			if seen[lit] {
				add(Finding{Code: CodeDuplicateLiteral, Reason: RefusedByNotation, ClassID: c.ID,
					Message: fmt.Sprintf("%s lists the literal %q twice.", c.Name, lit)})
			}
			seen[lit] = true
		}
	}

	validateIdentity(c, kind, add)
}

func validateAttributes(c *Class, classByName map[string]*Class, add func(Finding)) {
	seen := map[string]bool{}
	for _, a := range c.Attributes {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			add(Finding{Code: CodeMissingAttributeName, Reason: RefusedByNotation, ClassID: c.ID,
				Message: fmt.Sprintf("%s has an attribute with no name.", c.Name)})
			continue
		}
		if seen[name] {
			add(Finding{Code: CodeDuplicateAttribute, Reason: RefusedByNotation, ClassID: c.ID, Attribute: name,
				Message: fmt.Sprintf("%s has two attributes called %q. A write targeting that member could not say which it meant.", c.Name, name)})
			continue
		}
		seen[name] = true

		if _, ok := MultiplicityOf(a.Multiplicity); !ok {
			add(Finding{Code: CodeUnknownMultiplicity, Reason: RefusedOutOfSubset, ClassID: c.ID, Attribute: name,
				Message: fmt.Sprintf("Atlas does not author the multiplicity %q. This build has 0..1, 1, 0..* and 1..*.", a.Multiplicity)})
		}

		if _, ok := PrimitiveOf(a.Type); ok {
			continue
		}
		target, ok := classByName[a.Type]
		if !ok {
			add(Finding{Code: CodeUnknownType, Reason: RefusedByNotation, ClassID: c.ID, Attribute: name,
				Message: fmt.Sprintf("%s.%s is typed %q, and this model has no class of that name and no such primitive type.", c.Name, name, a.Type)})
			continue
		}
		// A class holding itself as a member has no finite value; the relationship it
		// wants is an association, which has multiplicities and can be walked.
		if target.ID == c.ID {
			add(Finding{Code: CodeAttributeTypedAsSelf, Reason: RefusedByNotation, ClassID: c.ID, Attribute: name,
				Message: fmt.Sprintf("%s.%s is typed as %s itself. Draw an association instead — a value that contains itself has no end.", c.Name, name, c.Name)})
		}
	}
}

func validateIdentity(c *Class, kind StereotypeKind, add func(Finding)) {
	if len(c.Identity) == 0 {
		return
	}
	if !kind.HasIdentity {
		add(Finding{Code: CodeIdentityNotAllowed, Reason: RefusedByNotation, ClassID: c.ID,
			Message: fmt.Sprintf("%s is a %s, and two of those with equal contents are the same value — there is nothing to identify. Only a business object declares a business key.", c.Name, strings.ToLower(kind.Label))})
		return
	}
	byName := map[string]Attribute{}
	for _, a := range c.Attributes {
		byName[a.Name] = a
	}
	for _, key := range c.Identity {
		a, ok := byName[key]
		if !ok {
			add(Finding{Code: CodeUnknownIdentityAttribute, Reason: RefusedByNotation, ClassID: c.ID, Attribute: key,
				Message: fmt.Sprintf("%s's business key names %q, which is not one of its attributes.", c.Name, key)})
			continue
		}
		mult, known := MultiplicityOf(a.Multiplicity)
		if !known {
			continue // already reported as an unknown multiplicity
		}
		if !mult.Required {
			add(Finding{Code: CodeIdentityNotRequired, Reason: RefusedByNotation, ClassID: c.ID, Attribute: key,
				Message: fmt.Sprintf("%s.%s is part of the business key but may be absent. A key that can be missing identifies nothing.", c.Name, key)})
		}
		if mult.Collection {
			add(Finding{Code: CodeIdentityNotSingular, Reason: RefusedByNotation, ClassID: c.ID, Attribute: key,
				Message: fmt.Sprintf("%s.%s is part of the business key but holds a list. A key has to be one value.", c.Name, key)})
		}
	}
}

func validateAssociations(m Model, classByID map[string]*Class, add func(Finding)) {
	seen := map[string]bool{}
	for _, a := range m.Associations {
		if seen[a.ID] {
			add(Finding{Code: CodeDuplicateAssociationID, Reason: RefusedByNotation, AssociationID: a.ID,
				Message: fmt.Sprintf("Two associations share the id %q.", a.ID)})
			continue
		}
		seen[a.ID] = true

		from, okFrom := classByID[a.From.ClassID]
		to, okTo := classByID[a.To.ClassID]
		if !okFrom || !okTo {
			missing := a.From.ClassID
			if okFrom {
				missing = a.To.ClassID
			}
			add(Finding{Code: CodeUnknownClassRef, Reason: RefusedByNotation, AssociationID: a.ID,
				Message: fmt.Sprintf("An association end names the class %q, which this model does not contain.", missing)})
			continue
		}
		for _, end := range []End{a.From, a.To} {
			if end.Multiplicity == "" {
				continue // an end may leave its multiplicity unsaid
			}
			if _, ok := MultiplicityOf(end.Multiplicity); !ok {
				add(Finding{Code: CodeUnknownMultiplicity, Reason: RefusedOutOfSubset, AssociationID: a.ID,
					Message: fmt.Sprintf("Atlas does not author the multiplicity %q. This build has 0..1, 1, 0..* and 1..*.", end.Multiplicity)})
			}
		}
		if a.Kind == KindGeneralization && from.ID == to.ID {
			add(Finding{Code: CodeSelfGeneralization, Reason: RefusedByNotation, AssociationID: a.ID,
				Message: fmt.Sprintf("%s is drawn as a kind of itself.", from.Name)})
			continue
		}
		if ok, refusal := AllowAssociation(a.Kind, from.Stereotype, to.Stereotype); !ok {
			add(Finding{Code: CodeRelationshipRefused, Reason: refusal.Reason, AssociationID: a.ID,
				Message: fmt.Sprintf("%s → %s: %s", from.Name, to.Name, refusal.Message)})
		}
	}
	for _, f := range generalizationCycles(m, classByID) {
		add(f)
	}
}

// generalizationCycles reports a specialization chain that closes on itself. It is
// the one check that is a property of the graph rather than of any single link:
// A is a kind of B and B is a kind of A is not wrong at either arrow.
func generalizationCycles(m Model, classByID map[string]*Class) []Finding {
	parents := map[string][]string{}
	edgeID := map[string]string{}
	for _, a := range m.Associations {
		if a.Kind != KindGeneralization || a.From.ClassID == a.To.ClassID {
			continue
		}
		if _, ok := classByID[a.From.ClassID]; !ok {
			continue
		}
		if _, ok := classByID[a.To.ClassID]; !ok {
			continue
		}
		parents[a.From.ClassID] = append(parents[a.From.ClassID], a.To.ClassID)
		edgeID[a.From.ClassID+">"+a.To.ClassID] = a.ID
	}

	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	color := map[string]int{}
	reported := map[string]bool{}
	out := []Finding{}
	var walk func(id string)
	walk = func(id string) {
		color[id] = grey
		for _, parent := range parents[id] {
			switch color[parent] {
			case grey:
				aid := edgeID[id+">"+parent]
				if !reported[aid] {
					reported[aid] = true
					out = append(out, Finding{
						Code: CodeGeneralizationCycle, Reason: RefusedByNotation, AssociationID: aid,
						Message: fmt.Sprintf("%s is a kind of %s, which is already a kind of %s. A specialization chain cannot close on itself.",
							classByID[id].Name, classByID[parent].Name, classByID[id].Name),
					})
				}
			case white:
				walk(parent)
			}
		}
		color[id] = black
	}
	ids := make([]string, 0, len(parents))
	for id := range parents {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic: the same model always reports the same edge
	for _, id := range ids {
		if color[id] == white {
			walk(id)
		}
	}
	return out
}

// validateStores checks what a store may be. Two of the rules are about the class it
// holds, and both follow from what a store is *for*: a process reads from one by
// naming which thing it wants, and the only thing that names one is a business key.
func validateStores(m Model, classByName map[string]*Class, add func(Finding)) {
	seenID := map[string]bool{}
	seenName := map[string]bool{}
	for _, st := range m.Stores {
		if seenID[st.ID] {
			add(Finding{Code: CodeDuplicateStoreID, Reason: RefusedByNotation, StoreID: st.ID,
				Message: fmt.Sprintf("Two data stores share the id %q.", st.ID)})
			continue
		}
		seenID[st.ID] = true

		name := strings.TrimSpace(st.Name)
		if name == "" {
			add(Finding{Code: CodeStoreMissingName, Reason: RefusedByNotation, StoreID: st.ID,
				Message: "A data store needs a name: it is what a process's <dataStore> refers to it by."})
			continue
		}
		if seenName[name] {
			add(Finding{Code: CodeDuplicateStoreName, Reason: RefusedByNotation, StoreID: st.ID,
				Message: fmt.Sprintf("Two data stores are called %q. A process naming that store could not say which it meant.", name)})
			continue
		}
		seenName[name] = true

		if _, ok := StoreModeOf(st.Mode); !ok {
			add(Finding{Code: CodeStoreUnknownMode, Reason: RefusedOutOfSubset, StoreID: st.ID,
				Message: fmt.Sprintf("Atlas does not author %q data stores. This build reads from a store; writing through one is a transaction against something outside the engine and is not authored yet.", st.Mode)})
		}

		class, ok := classByName[st.Class]
		if !ok {
			add(Finding{Code: CodeStoreUnknownClass, Reason: RefusedByNotation, StoreID: st.ID,
				Message: fmt.Sprintf("%s holds %q, and this model has no class of that name. A store keeps instances of something; say what.", name, st.Class)})
			continue
		}
		if class.Stereotype != StereotypeBusinessObject {
			kind, _ := stereotypeOf(class.Stereotype)
			add(Finding{Code: CodeStoreClassNotStorable, Reason: RefusedByNotation, StoreID: st.ID,
				Message: fmt.Sprintf("%s holds %s, which is a %s. Only a business object outlives the process that made it — the others are values, and a value is kept inside whatever holds it.", name, class.Name, strings.ToLower(kind.Label))})
			continue
		}
		if len(class.Identity) == 0 {
			add(Finding{Code: CodeStoreClassHasNoKey, Reason: RefusedByNotation, StoreID: st.ID,
				Message: fmt.Sprintf("%s holds %s, which declares no business key. A process reads from a store by naming which thing it wants, and nothing here names one — give %s an identity.", name, class.Name, class.Name)})
		}
	}
}

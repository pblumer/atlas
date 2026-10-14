package infomodel

import (
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// Where a business object is used, and how
// (ADR-0338).
//
// ADR-0230 gave `itemSubjectRef` something to resolve against, and every reading
// since has run from the process outwards: this process's data flow, this
// application's derived model, this instance's object graph. The vocabulary itself
// has had no reading of its own. Somebody maintaining an Order — adding a member,
// renaming a state, retiring an enumeration — could see what an Order *is* and
// nothing at all about what would break.
//
// This reads the other direction: one class, and every place it is used. It is the
// question a change asks, and it cannot be answered by looking at the class.
//
// Two kinds of use, kept apart because a reader acts on them differently:
//
//   - A **process** uses a class when a data object declares it — and then reads it,
//     writes a member of it, moves its state, or names the store it is kept in. This
//     is what "used" means to somebody about to change a member's name.
//   - The **model** uses a class when another class is typed with it, an association
//     joins the two, a lifecycle takes its states from it, or a store holds it. For an
//     «enumeration» this is usually the *only* kind of use there is, which is exactly
//     why a reading that looked only at processes would report the vocabulary's most
//     shared elements as unused.
//
// Everything here is computed, never stored: both sides are read fresh and matched by
// name, for the reason the difference reading is stateless (ADR-0310) — the names are
// already the mechanism, so there is no second identity to keep and nothing to keep it
// in.
//
// **What it does not see.** Only the processes the caller hands in, which is the set
// an application actually runs — the deployed, active, latest version of each. A draft
// in the Modeler is not in it: a draft has not been compiled, and compiling every draft
// to answer a list would make the answer depend on whether somebody's work in progress
// parses. A "used by nothing" therefore means nothing *deployed*, and the view says so
// rather than leaving the reader to assume the stronger claim.

// The kinds of use a process makes of a class. They are stable machine names for the
// same reason the rule slugs are: a view groups and filters by them without parsing
// prose.
const (
	// ProcessUseDeclare is a <dataObject> whose itemSubjectRef names this class. It is
	// the use every other one hangs off, and it names no element: a data object is not
	// a flow node (ADR-0053).
	ProcessUseDeclare = "declare"
	// ProcessUseRead is a <dataInputAssociation>: an activity reads the object into a
	// process variable its FEEL can then see (ADR-0059).
	ProcessUseRead = "read"
	// ProcessUseWrite is a <dataOutputAssociation>: an activity writes the object, or
	// one member of it (ADR-0060), and may move its data state (ADR-0058).
	ProcessUseWrite = "write"
	// ProcessUseStore is a <dataStoreReference> naming a store that holds this class —
	// the cross-process channel, where instances outlive the process that made them.
	ProcessUseStore = "store"
)

// The kinds of use the information model makes of a class.
const (
	// ModelUseAttribute is another class carrying an attribute of this type. For an
	// «enumeration» it is the ordinary case and usually the only one.
	ModelUseAttribute = "attribute"
	// ModelUseAssociation is an association with this class at one end.
	ModelUseAssociation = "association"
	// ModelUseStates is a lifecycle whose states are this «enumeration»'s literals
	// (ADR-0306).
	ModelUseStates = "states"
	// ModelUseStore is a data store holding this class's instances.
	ModelUseStore = "store"
)

// Process is one compiled process with the two labels a usage row shows that the
// compiled form does not carry. A CompiledProcess knows its key and its BPMN process
// id; what it is *called* and which version is deployed are facts of the deployment,
// so the caller states them rather than this package inventing a lookup for them.
type Process struct {
	Compiled *compiler.CompiledProcess
	Name     string
	Version  int32
}

// ProcessUse is one place in one process where a class is used, located precisely
// enough to act on: which element, which member, which state.
type ProcessUse struct {
	ProcessDefinitionKey uint64 `json:"processDefinitionKey"`
	ProcessID            string `json:"processId"`
	ProcessName          string `json:"processName,omitempty"`
	Version              int32  `json:"version,omitempty"`
	Kind                 string `json:"kind"`
	// Object is the data object's name in that process — the local string BPMN scopes
	// to one definition, and the reason this whole area exists.
	Object string `json:"object,omitempty"`
	// ElementID is the BPMN id of the element making the use. Empty for a declaration,
	// which is a property of the process rather than of any element in it.
	ElementID string `json:"elementId,omitempty"`
	// Attribute is the member a write targets (ADR-0060); empty for a write of the
	// whole value.
	Attribute string `json:"attribute,omitempty"`
	// State is the state a write moves the object into, or — on a declaration — the
	// state instances of it are created in.
	State string `json:"state,omitempty"`
	// Variable is the process variable a read lands in (ADR-0059).
	Variable string `json:"variable,omitempty"`
	// WritesValue reports whether a write carries a value at all. A write without one
	// is a state-only transition, which is a different act and reads as one.
	WritesValue bool `json:"writesValue,omitempty"`
	// Collection marks a data object declared as a collection.
	Collection bool `json:"collection,omitempty"`
	// Store is the data store a ProcessUseStore names.
	Store string `json:"store,omitempty"`
}

// ModelUse is one place in an information model where a class is used — the half of
// "where used" that is about the vocabulary rather than about a process.
type ModelUse struct {
	ModelID   string `json:"modelId"`
	ModelName string `json:"modelName"`
	Kind      string `json:"kind"`
	// Class is the class making the use: the one carrying the attribute or the
	// lifecycle, or the one at the other end of the association. Empty for a store,
	// which belongs to no class.
	Class string `json:"class,omitempty"`
	// Name is the attribute's name or the association's, where it has one.
	Name string `json:"name,omitempty"`
	// Detail is the reading a person wants beside it — a multiplicity, an
	// association's kind and its ends, a store's mode and Worker.
	Detail string `json:"detail,omitempty"`
}

// UsageSummary is the shape of the answer before its rows: what a list column can
// show, and what the detail view says above its tables.
type UsageSummary struct {
	Processes int `json:"processes"`
	Uses      int `json:"uses"`
	Reads     int `json:"reads"`
	Writes    int `json:"writes"`
	// Attributes and States are what processes *actually* touch, which is rarely all
	// of what the class declares — the gap between the two is the interesting part,
	// and it is the same gap the difference reading reports per application.
	Attributes []string `json:"attributes,omitempty"`
	States     []string `json:"states,omitempty"`
	Stores     []string `json:"stores,omitempty"`
	ModelUses  int      `json:"modelUses"`
}

// Usage is everywhere one class is used, with the class itself, so a detail view is
// one call rather than two that can come to disagree.
type Usage struct {
	Class         Class  `json:"class"`
	ModelID       string `json:"modelId"`
	ModelName     string `json:"modelName"`
	ApplicationID string `json:"applicationId"`

	Processes []ProcessUse `json:"processes"`
	Model     []ModelUse   `json:"model"`
	Summary   UsageSummary `json:"summary"`
}

// CatalogEntry is one class as the overview lists it: what it is, what it holds, and
// how much of the estate depends on it.
type CatalogEntry struct {
	ModelID       string `json:"modelId"`
	ModelName     string `json:"modelName"`
	ApplicationID string `json:"applicationId"`
	ClassID       string `json:"classId"`
	Name          string `json:"name"`
	Stereotype    string `json:"stereotype"`
	Documentation string `json:"documentation,omitempty"`
	// Members is the count of whichever members this kind of class has: attributes,
	// or an «enumeration»'s literals. One column, honestly filled, rather than two of
	// which one is always empty.
	Members   int          `json:"members"`
	Identity  []string     `json:"identity,omitempty"`
	States    int          `json:"states"`
	Usage     UsageSummary `json:"usage"`
	UpdatedAt int64        `json:"updatedAt"`
}

// Catalog lists every class of every model handed in, each with the counts of where
// it is used.
//
// processes is asked once per application, not once per class: an application's
// processes are walked a single time and bucketed by the class each use resolves to,
// so a catalogue of fifty classes costs one walk rather than fifty. It may be nil,
// which reads every class against no process at all — the model-side uses still stand,
// and for an enumeration they are the whole answer.
func Catalog(models []Model, processes func(applicationID string) []Process) []CatalogEntry {
	out := []CatalogEntry{}
	for _, applicationID := range applicationsOf(models) {
		siblings := modelsOf(models, applicationID)
		var procs []Process
		if processes != nil {
			procs = processes(applicationID)
		}
		index := indexProcesses(procs, storeClasses(siblings))
		for _, m := range siblings {
			for _, c := range m.Classes {
				out = append(out, CatalogEntry{
					ModelID: m.ID, ModelName: m.Name, ApplicationID: m.ApplicationID,
					ClassID: c.ID, Name: c.Name, Stereotype: c.Stereotype,
					Documentation: c.Documentation,
					Members:       memberCount(c), Identity: c.Identity, States: stateCount(c),
					Usage:     summarizeUses(index[c.Name], len(modelUses(c, m.ID, siblings))),
					UpdatedAt: m.UpdatedAt,
				})
			}
		}
	}
	// By name, because the list is read as a vocabulary and a vocabulary is
	// alphabetical. The model breaks a tie, so two applications that both model an
	// Order sit together and the row says which is which.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.ModelName != b.ModelName {
			return a.ModelName < b.ModelName
		}
		return a.ModelID < b.ModelID
	})
	return out
}

// UsageOf reads everywhere one class is used. models must contain the model that holds
// the class and may contain any others — only its own application's are read, since
// that is the namespace its name resolves in — and procs are that application's
// processes. It reports false for a model or a class that is not there, rather than an
// empty usage, because "used nowhere" is a claim about something that exists.
func UsageOf(models []Model, modelID, className string, procs []Process) (Usage, bool) {
	var owner *Model
	for i := range models {
		if models[i].ID == modelID {
			owner = &models[i]
			break
		}
	}
	if owner == nil {
		return Usage{}, false
	}
	class, ok := owner.ClassByName(className)
	if !ok {
		return Usage{}, false
	}
	siblings := modelsOf(models, owner.ApplicationID)
	uses := indexProcesses(procs, storeClasses(siblings))[class.Name]
	if uses == nil {
		uses = []ProcessUse{}
	}
	model := modelUses(*class, owner.ID, siblings)
	return Usage{
		Class: *class, ModelID: owner.ID, ModelName: owner.Name,
		ApplicationID: owner.ApplicationID,
		Processes:     uses,
		Model:         model,
		Summary:       summarizeUses(uses, len(model)),
	}, true
}

// indexProcesses walks every process once and buckets every use by the class name it
// resolves to. Walking per class instead would re-read the same graphs once per class
// in the vocabulary, for an answer that is a partition of one walk.
//
// A data object with no itemSubjectRef resolves to no class and contributes nothing.
// Reading its *name* as a class name is what the derived model does, deliberately and
// with the guess stated as a gap (ADR-0301); doing it silently here would put an
// inference in a list a person is about to act on.
func indexProcesses(procs []Process, storeClass map[string]string) map[string][]ProcessUse {
	index := map[string][]ProcessUse{}
	for _, p := range procs {
		cp := p.Compiled
		if cp == nil {
			continue
		}
		row := func(kind string) ProcessUse {
			return ProcessUse{
				ProcessDefinitionKey: cp.Key, ProcessID: cp.ProcessId(),
				ProcessName: p.Name, Version: p.Version, Kind: kind,
			}
		}
		add := func(class string, u ProcessUse) {
			if class == "" {
				return
			}
			index[class] = append(index[class], u)
		}

		// The declarations first, and the object→class map they imply: an association
		// names an object, and only the declaration says what that object is.
		objectClass := map[string]string{}
		for _, do := range cp.DataObjects() {
			class, object := cp.Intern(do.ItemType), cp.Intern(do.Name)
			if class == "" || object == "" {
				continue
			}
			objectClass[object] = class
			u := row(ProcessUseDeclare)
			u.Object, u.State, u.Collection = object, cp.Intern(do.InitialState), do.IsCollection
			add(class, u)
		}

		// Then the elements, in the order a token would meet them. A node's reads come
		// before its writes because that is the order they happen in: a read binds the
		// object on activation, a write lands on completion.
		for id := int32(0); int(id) < cp.NodeCount(); id++ {
			element := cp.ElementBpmnId(id)
			for _, a := range cp.DataInputAssociations(id) {
				object := cp.Intern(a.DataObject)
				u := row(ProcessUseRead)
				u.Object, u.ElementID, u.Variable = object, element, cp.Intern(a.Variable)
				add(objectClass[object], u)
			}
			for _, a := range cp.DataOutputAssociations(id) {
				object := cp.Intern(a.DataObject)
				// One row per write rather than per arrow: an arrow may set several
				// members at once (ADR-0350), and
				// each of them is a use of a different attribute. An arrow with no writes
				// is ADR-0058's state-only transition — still a use of the object, and
				// still the one row it has always been.
				if len(a.Writes) == 0 {
					u := row(ProcessUseWrite)
					u.Object, u.ElementID = object, element
					u.State = cp.Intern(a.TargetState)
					add(objectClass[object], u)
					continue
				}
				for _, w := range a.Writes {
					u := row(ProcessUseWrite)
					u.Object, u.ElementID = object, element
					u.Attribute, u.State = cp.Intern(w.TargetPath), cp.Intern(a.TargetState)
					u.WritesValue = true
					add(objectClass[object], u)
				}
			}
		}

		// And the stores. Which class a store holds is a fact of the model, not of the
		// process — the process names the store and nothing else.
		for _, ds := range cp.DataStores() {
			store := cp.Intern(ds.Name)
			u := row(ProcessUseStore)
			u.Store, u.ElementID = store, cp.Intern(ds.ElementId)
			add(storeClass[store], u)
		}
	}
	return index
}

// modelUses reads the vocabulary's own uses of a class, across every model of the
// application. An application's models share one namespace — a data object's type
// resolves against all of them (see NewVocabulary) — so an attribute in a second model
// typed with this class is a use of it, and saying otherwise would hide the use that
// crosses two documents.
func modelUses(class Class, ownerModelID string, models []Model) []ModelUse {
	out := []ModelUse{}
	for _, m := range models {
		for _, c := range m.Classes {
			for _, a := range c.Attributes {
				if a.Type != class.Name {
					continue
				}
				// The detail column is read, not parsed, so it says what the number
				// means: a bare "1" beside an attribute is a multiplicity to whoever
				// wrote it and a mystery to everybody else.
				detail := ""
				if a.Multiplicity != "" {
					detail = "multiplicity " + a.Multiplicity
				}
				out = append(out, ModelUse{
					ModelID: m.ID, ModelName: m.Name, Kind: ModelUseAttribute,
					Class: c.Name, Name: a.Name, Detail: detail,
				})
			}
			if c.Lifecycle != nil && c.Lifecycle.StatesFrom == class.Name {
				out = append(out, ModelUse{
					ModelID: m.ID, ModelName: m.Name, Kind: ModelUseStates, Class: c.Name,
				})
			}
		}
		// Association ends name a class by its stable id, which is scoped to the model
		// that holds it — so only this class's own model can join it to anything, and an
		// id that repeats in a second document is a different class with the same handle.
		for _, a := range associationsOf(m, ownerModelID) {
			from, fromOK := m.ClassByID(a.From.ClassID)
			to, toOK := m.ClassByID(a.To.ClassID)
			if !fromOK || !toOK {
				continue // a dangling end; validation refuses one on write
			}
			if from.ID != class.ID && to.ID != class.ID {
				continue
			}
			other := from.Name
			if from.ID == class.ID {
				other = to.Name
			}
			out = append(out, ModelUse{
				ModelID: m.ID, ModelName: m.Name, Kind: ModelUseAssociation,
				Class: other, Name: a.Name,
				Detail: a.Kind + " · " + endLabel(from.Name, a.From.Multiplicity) +
					" → " + endLabel(to.Name, a.To.Multiplicity),
			})
		}
		for _, s := range m.Stores {
			if s.Class != class.Name {
				continue
			}
			detail := s.Mode
			if s.Worker != "" {
				detail = strings.TrimPrefix(detail+" · "+s.Worker, " · ")
			}
			out = append(out, ModelUse{
				ModelID: m.ID, ModelName: m.Name, Kind: ModelUseStore, Name: s.Name, Detail: detail,
			})
		}
	}
	return out
}

// associationsOf is m's associations when m is the model that owns the class being
// read, and none otherwise — the guard that keeps a repeated class id in another
// document from resolving to this class.
func associationsOf(m Model, ownerModelID string) []Association {
	if m.ID != ownerModelID {
		return nil
	}
	return m.Associations
}

// endLabel reads one end of an association as a person would say it: the class, and
// how many of it there are where the model states that.
func endLabel(class, multiplicity string) string {
	if multiplicity == "" {
		return class
	}
	return class + " " + multiplicity
}

// summarizeUses counts the kinds apart. The distinct members and states are sorted,
// because a summary that reorders between two reads of an unchanged model reads as a
// model that changed.
func summarizeUses(uses []ProcessUse, modelUses int) UsageSummary {
	s := UsageSummary{Uses: len(uses), ModelUses: modelUses}
	processes, attributes, states, stores := map[uint64]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range uses {
		processes[u.ProcessDefinitionKey] = true
		switch u.Kind {
		case ProcessUseRead:
			s.Reads++
		case ProcessUseWrite:
			s.Writes++
		}
		if u.Attribute != "" {
			attributes[u.Attribute] = true
		}
		if u.State != "" {
			states[u.State] = true
		}
		if u.Store != "" {
			stores[u.Store] = true
		}
	}
	s.Processes = len(processes)
	s.Attributes, s.States, s.Stores = sortedKeys(attributes), sortedKeys(states), sortedKeys(stores)
	return s
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// storeClasses maps each declared store to the class it holds, which is the only
// thing that turns a <dataStoreReference> in a process into a use of a class.
func storeClasses(models []Model) map[string]string {
	out := map[string]string{}
	for _, m := range models {
		for _, s := range m.Stores {
			if s.Class != "" {
				out[s.Name] = s.Class
			}
		}
	}
	return out
}

// applicationsOf lists the applications the models belong to, in the order they are
// first met, so a caller's ordering survives into the grouping.
func applicationsOf(models []Model) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range models {
		if seen[m.ApplicationID] {
			continue
		}
		seen[m.ApplicationID] = true
		out = append(out, m.ApplicationID)
	}
	return out
}

func modelsOf(models []Model, applicationID string) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if m.ApplicationID == applicationID {
			out = append(out, m)
		}
	}
	return out
}

func memberCount(c Class) int {
	if c.Stereotype == StereotypeEnumeration {
		return len(c.Literals)
	}
	return len(c.Attributes)
}

func stateCount(c Class) int {
	if c.Lifecycle == nil {
		return 0
	}
	return len(c.Lifecycle.States)
}

package archsketch

import (
	"cmp"
	"slices"
	"strings"
)

// The data layer of a release's architecture (ADR-0495 slice C): what its processes
// keep and read, the classes they keep it as, and the personal data they declare.
//
// Everything here is read off the release's own compiled processes, and the class
// names checked against the application's information model, at the moment the
// snapshot is taken. A process reader may see all of it: a data object, a store and a
// personal-data declaration are things the process model states (ADR-0495 §5).
// Personal data is recorded as a declaration — which variables, about whom, and who is
// handed them — and never as a value (ADR-0314).

// The bounds on each recorded list (ADR-0495 §6). They were measured on a real
// installation: there, no process declared more than two data objects and six personal
// variables, and no application more than eighteen distinct personal variables. Real
// models are that small, so each bound is a guard against a generated or pathological
// model, set well above what was seen, and what a bound leaves out is counted in
// [Data.Omitted] rather than dropped in silence.
const (
	maxDataObjects       = 200
	maxDataStores        = 100
	maxDataClasses       = 100
	maxPersonalVariables = 100
)

// Data is the data layer, absent on a snapshot taken before format version 5. A
// snapshot that has one recorded the layer even when every list is empty: the release
// was read and declares no data.
type Data struct {
	Objects  []DataObject          `json:"objects"`
	Stores   []DataStore           `json:"stores"`
	Classes  []DataClass           `json:"classes"`
	Personal []PersonalDeclaration `json:"personal"`
	// Omitted counts, per kind, what the bounds left out.
	Omitted DataOmitted `json:"omitted,omitzero"`
}

// DataOmitted counts what the bounds left out of each list.
type DataOmitted struct {
	Objects           int `json:"objects,omitempty"`
	Stores            int `json:"stores,omitempty"`
	Classes           int `json:"classes,omitempty"`
	PersonalVariables int `json:"personalVariables,omitempty"`
}

// text lists what the bounds left out.
func (o DataOmitted) text() string {
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{
		{o.Objects, "data object", "data objects"},
		{o.Stores, "data store", "data stores"},
		{o.Classes, "class", "classes"},
		{o.PersonalVariables, "personal variable", "personal variables"},
	} {
		if c.n > 0 {
			parts = append(parts, plural(c.n, c.one, c.many))
		}
	}
	return strings.Join(parts, ", ")
}

// DataObject is one BPMN data object a process declares (ADR-0053): its type, the
// state it starts in, and which activities read it and write it.
type DataObject struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	// Class is the type the model declares for it (the itemDefinition's structure),
	// empty when it declares none.
	Class        string `json:"class,omitempty"`
	Collection   bool   `json:"collection,omitempty"`
	InitialState string `json:"initialState,omitempty"`
	// ReadBy are the elements whose data input associations read it.
	ReadBy []string `json:"readBy"`
	// WrittenBy are the elements whose data output associations write it.
	WrittenBy []DataWrite `json:"writtenBy"`
}

// DataWrite is one element writing a data object: the state it moves the object into,
// and the members it writes when it writes members rather than the whole value.
type DataWrite struct {
	ElementID string   `json:"elementId"`
	State     string   `json:"state,omitempty"`
	Members   []string `json:"members,omitempty"`
}

// DataStore is one named data store the release's processes refer to: a place
// instances of a class outlive the process that made them. The compiled model keeps
// where a store is named, not which activity reads or writes it.
type DataStore struct {
	Name string `json:"name"`
	// Class is the class the application's information model keeps in the store,
	// empty when no model names one.
	Class  string `json:"class,omitempty"`
	UsedBy []Use  `json:"usedBy"`
}

// DataClass is one class the release keeps data as: a type its data objects declare,
// or a class its stores hold.
type DataClass struct {
	Name string `json:"name"`
	// Modeled is set when the application's information model defines the class; a
	// class that is not modeled is only a name the process model declares.
	Modeled bool `json:"modeled,omitempty"`
}

// PersonalDeclaration is what one process declares about personal data (ADR-0314):
// which of its variables hold it, the variable naming whose data it is, and who the
// process hands work to.
type PersonalDeclaration struct {
	ProcessID string   `json:"processId"`
	Variables []string `json:"variables"`
	Subject   string   `json:"subject"`
	// HandedTo are the workers, runtime workers and job types the process hands work
	// to. Each can read every variable visible at its task in plaintext — a worker
	// process leasing a job is given them all deciphered, and a Worker Atlas runs
	// resolves its task over them — so each can receive these values, whether or not
	// its task names one.
	HandedTo []PersonalRecipient `json:"handedTo"`
}

// PersonalRecipient is one worker or job type a process hands work to, as its
// dependency is recorded.
type PersonalRecipient struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	WorkerType string `json:"workerType,omitempty"`
}

// normalize puts every list in a fixed order, merges stores named by several
// processes, and applies the bounds, so the same release produces the same layer.
func (d *Data) normalize() {
	for i := range d.Objects {
		o := &d.Objects[i]
		slices.Sort(o.ReadBy)
		o.ReadBy = slices.Compact(o.ReadBy)
		if o.ReadBy == nil {
			o.ReadBy = []string{}
		}
		for j := range o.WrittenBy {
			slices.Sort(o.WrittenBy[j].Members)
			o.WrittenBy[j].Members = slices.Compact(o.WrittenBy[j].Members)
		}
		slices.SortFunc(o.WrittenBy, func(a, b DataWrite) int {
			return cmp.Or(cmp.Compare(a.ElementID, b.ElementID), cmp.Compare(a.State, b.State),
				cmp.Compare(strings.Join(a.Members, ","), strings.Join(b.Members, ",")))
		})
		o.WrittenBy = slices.CompactFunc(o.WrittenBy, func(a, b DataWrite) bool {
			return a.ElementID == b.ElementID && a.State == b.State && slices.Equal(a.Members, b.Members)
		})
		if o.WrittenBy == nil {
			o.WrittenBy = []DataWrite{}
		}
	}
	slices.SortFunc(d.Objects, func(a, b DataObject) int {
		return cmp.Or(cmp.Compare(a.ProcessID, b.ProcessID), cmp.Compare(a.Name, b.Name))
	})
	d.Objects = slices.CompactFunc(d.Objects, func(a, b DataObject) bool {
		return a.ProcessID == b.ProcessID && a.Name == b.Name
	})

	stores := map[string]*DataStore{}
	for _, st := range d.Stores {
		if have, ok := stores[st.Name]; ok {
			have.UsedBy = append(have.UsedBy, st.UsedBy...)
			have.Class = cmp.Or(have.Class, st.Class)
			continue
		}
		cp := st
		cp.UsedBy = append([]Use(nil), st.UsedBy...)
		stores[st.Name] = &cp
	}
	d.Stores = d.Stores[:0]
	for _, st := range stores {
		st.UsedBy = normalizeUses(st.UsedBy)
		if st.UsedBy == nil {
			st.UsedBy = []Use{}
		}
		d.Stores = append(d.Stores, *st)
	}
	slices.SortFunc(d.Stores, func(a, b DataStore) int { return cmp.Compare(a.Name, b.Name) })

	slices.SortFunc(d.Classes, func(a, b DataClass) int { return cmp.Compare(a.Name, b.Name) })
	d.Classes = slices.CompactFunc(d.Classes, func(a, b DataClass) bool {
		if a.Name == b.Name {
			a.Modeled = a.Modeled || b.Modeled
			return true
		}
		return false
	})

	for i := range d.Personal {
		p := &d.Personal[i]
		slices.Sort(p.Variables)
		p.Variables = slices.Compact(p.Variables)
		if n := len(p.Variables) - maxPersonalVariables; n > 0 {
			p.Variables = p.Variables[:maxPersonalVariables]
			d.Omitted.PersonalVariables += n
		}
		slices.SortFunc(p.HandedTo, func(a, b PersonalRecipient) int {
			return cmp.Or(cmp.Compare(dependencyRank(a.Kind), dependencyRank(b.Kind)),
				cmp.Compare(a.Name, b.Name), cmp.Compare(a.WorkerType, b.WorkerType))
		})
		p.HandedTo = slices.Compact(p.HandedTo)
		if p.HandedTo == nil {
			p.HandedTo = []PersonalRecipient{}
		}
	}
	slices.SortFunc(d.Personal, func(a, b PersonalDeclaration) int { return cmp.Compare(a.ProcessID, b.ProcessID) })

	d.Objects, d.Omitted.Objects = bound(d.Objects, maxDataObjects, d.Omitted.Objects)
	d.Stores, d.Omitted.Stores = bound(d.Stores, maxDataStores, d.Omitted.Stores)
	d.Classes, d.Omitted.Classes = bound(d.Classes, maxDataClasses, d.Omitted.Classes)
	// An empty list is written as one, so a reader tells "none" from "not recorded".
	if d.Objects == nil {
		d.Objects = []DataObject{}
	}
	if d.Stores == nil {
		d.Stores = []DataStore{}
	}
	if d.Classes == nil {
		d.Classes = []DataClass{}
	}
	if d.Personal == nil {
		d.Personal = []PersonalDeclaration{}
	}
}

// bound keeps the first max entries of a sorted list and adds what it left out to the
// count already recorded.
func bound[T any](list []T, max, omitted int) ([]T, int) {
	if len(list) <= max {
		return list, omitted
	}
	return list[:max], omitted + len(list) - max
}

// states lists the states a data object starts in or is moved into, in the order
// they first appear: its initial state, then the writes'.
func (o DataObject) states() []string {
	var out []string
	if o.InitialState != "" {
		out = append(out, o.InitialState)
	}
	for _, w := range o.WrittenBy {
		if w.State != "" && !slices.Contains(out, w.State) {
			out = append(out, w.State)
		}
	}
	return out
}

// accessType is how the processes use a data object, in the words of an ArchiMate
// access relationship.
func (o DataObject) accessType() string {
	switch r, w := len(o.ReadBy) > 0, len(o.WrittenBy) > 0; {
	case r && w:
		return "ReadWrite"
	case r:
		return "Read"
	case w:
		return "Write"
	}
	return "Access"
}

// useText says how often a data object is written and read, for a box's third line.
func (o DataObject) useText() string {
	var parts []string
	if n := len(o.WrittenBy); n > 0 {
		parts = append(parts, plural(n, "write", "writes"))
	}
	if n := len(o.ReadBy); n > 0 {
		parts = append(parts, plural(n, "read", "reads"))
	}
	if len(parts) == 0 {
		return "no association reads or writes it"
	}
	return strings.Join(parts, ", ")
}

// useDetail names the elements that write and read a data object, for its tooltip.
func (o DataObject) useDetail() string {
	var parts []string
	if len(o.WrittenBy) > 0 {
		writes := make([]string, 0, len(o.WrittenBy))
		for _, w := range o.WrittenBy {
			text := w.ElementID
			if len(w.Members) > 0 {
				text += " (" + strings.Join(w.Members, ", ") + ")"
			}
			if w.State != "" {
				text += " → " + w.State
			}
			writes = append(writes, text)
		}
		parts = append(parts, "written at "+strings.Join(writes, ", "))
	}
	if len(o.ReadBy) > 0 {
		parts = append(parts, "read at "+strings.Join(o.ReadBy, ", "))
	}
	if len(parts) == 0 {
		return "no association reads or writes it"
	}
	return strings.Join(parts, "; ")
}

// keptAs says what keeps a class: the data objects declaring it and the stores
// holding it.
func (d *Data) keptAs(class string) string {
	objects, stores := 0, 0
	for _, o := range d.Objects {
		if o.Class == class {
			objects++
		}
	}
	for _, st := range d.Stores {
		if st.Class == class {
			stores++
		}
	}
	var parts []string
	if objects > 0 {
		parts = append(parts, "as "+plural(objects, "data object", "data objects"))
	}
	if stores > 0 {
		parts = append(parts, "in "+plural(stores, "store", "stores"))
	}
	return strings.Join(parts, " and ")
}

// recipientText names one recipient the way the sketch lists dependencies.
func (r PersonalRecipient) text() string {
	switch r.Kind {
	case DependencyJobType:
		return "job type " + r.Name
	case DependencyRuntimeWorker:
		return "a worker decided at run time"
	}
	return r.Name
}

// handedToText says who a declaring process hands its personal data to.
func (p PersonalDeclaration) handedToText() string {
	if len(p.HandedTo) == 0 {
		return "handed to no worker"
	}
	names := make([]string, 0, len(p.HandedTo))
	for _, r := range p.HandedTo {
		names = append(names, r.text())
	}
	return "readable in plaintext by " + strings.Join(names, ", ")
}

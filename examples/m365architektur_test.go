package examples

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/api/infomodel"
)

// The architecture of m365-dienste (examples/m365-dienste/architektur): an information
// model, capabilities and value streams that install-m365-dienste.py files on the server,
// and the data objects and stores the processes declare. Nothing at deploy refuses a
// data object whose class or state the model does not know (ADR-0259 §5 warns only), so
// these tests are what keeps the three in step.

const m365Dir = "m365-dienste"

func m365InfoModel(t *testing.T) infomodel.Model {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m365Dir, "architektur", "informationsmodell.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m infomodel.Model
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// What the installer sends is the import: it must arrive whole, with nothing
	// dropped or adjusted on the way.
	res, err := infomodel.ParseImport(infomodel.ImportFormatJSON, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Notes {
		if n.Level != infomodel.NoteInfo {
			t.Errorf("the import %s %s: %s", n.Level, n.Element, n.Message)
		}
	}
	return m
}

// TestM365InformationModelIsValid: the model passes the same validation a save does.
func TestM365InformationModelIsValid(t *testing.T) {
	m := m365InfoModel(t)
	v := infomodel.Validate(m)
	for _, f := range v.Findings {
		t.Errorf("%s (%s %s%s %s): %s", f.Code, f.ClassID, f.AssociationID, f.StoreID, f.State, f.Message)
	}
	if !v.Valid {
		t.Fatal("the information model of m365-dienste does not validate")
	}
}

type m365Defs struct {
	Items []struct {
		ID           string `xml:"id,attr"`
		StructureRef string `xml:"structureRef,attr"`
	} `xml:"itemDefinition"`
	Stores []struct {
		ID   string `xml:"id,attr"`
		Name string `xml:"name,attr"`
	} `xml:"dataStore"`
	Process struct {
		ID      string `xml:"id,attr"`
		Objects []struct {
			ID             string `xml:"id,attr"`
			Name           string `xml:"name,attr"`
			ItemSubjectRef string `xml:"itemSubjectRef,attr"`
		} `xml:"dataObject"`
		Refs []struct {
			ID            string `xml:"id,attr"`
			DataObjectRef string `xml:"dataObjectRef,attr"`
			State         struct {
				Name string `xml:"name,attr"`
			} `xml:"dataState"`
		} `xml:"dataObjectReference"`
		StoreRefs []struct {
			ID           string `xml:"id,attr"`
			Name         string `xml:"name,attr"`
			DataStoreRef string `xml:"dataStoreRef,attr"`
		} `xml:"dataStoreReference"`
	} `xml:"process"`
}

// TestM365ProcessDataMatchesTheModel: every data object a process declares is typed by a
// class of the model, every state it writes is a state of that class's lifecycle, and
// every data store it names is a store of the model.
func TestM365ProcessDataMatchesTheModel(t *testing.T) {
	m := m365InfoModel(t)
	classes := map[string]infomodel.Class{}
	for _, c := range m.Classes {
		classes[c.Name] = c
	}
	stores := map[string]bool{}
	for _, s := range m.Stores {
		stores[s.Name] = true
	}
	files, _ := filepath.Glob(filepath.Join(m365Dir, "*.bpmn"))
	if len(files) == 0 {
		t.Fatal("no processes")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var d m365Defs
		if err := xml.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(d.Process.Objects) == 0 {
			t.Errorf("%s declares no data object", f)
		}
		item := map[string]string{}
		for _, it := range d.Items {
			item[it.ID] = it.StructureRef
		}
		class := map[string]string{}
		for _, o := range d.Process.Objects {
			c, ok := item[o.ItemSubjectRef]
			if !ok {
				t.Errorf("%s: data object %s names no itemDefinition (%q)", f, o.Name, o.ItemSubjectRef)
				continue
			}
			if _, ok := classes[c]; !ok {
				t.Errorf("%s: data object %s is typed %s, which the information model does not define", f, o.Name, c)
			}
			class[o.ID] = c
		}
		for _, r := range d.Process.Refs {
			c := classes[class[r.DataObjectRef]]
			if r.State.Name == "" {
				continue
			}
			if c.Lifecycle == nil || !slices.ContainsFunc(c.Lifecycle.States, func(s infomodel.LifecycleState) bool { return s.Name == r.State.Name }) {
				t.Errorf("%s: %s writes the state %q, which the lifecycle of %s does not have", f, r.ID, r.State.Name, c.Name)
			}
		}
		named := map[string]string{}
		for _, s := range d.Stores {
			named[s.ID] = s.Name
		}
		for _, r := range d.Process.StoreRefs {
			if !stores[named[r.DataStoreRef]] {
				t.Errorf("%s: data store %q is not a store of the information model", f, named[r.DataStoreRef])
			}
		}
	}
}

type m365Architecture struct {
	Capabilities []capability.Capability  `json:"capabilities"`
	ValueStreams []capability.ValueStream `json:"valueStreams"`
}

// TestM365CapabilitiesAreValid: each capability and value stream passes the server's
// validation, every process realization of the online application names one of its
// processes, every process is realized by a capability, and every dependency and stage
// names a capability of the file.
func TestM365CapabilitiesAreValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(m365Dir, "architektur", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a m365Architecture
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	manRaw, err := os.ReadFile(filepath.Join(m365Dir, "atlas.json"))
	if err != nil {
		t.Fatal(err)
	}
	var man struct {
		Key       string
		Processes []struct{ ID string }
	}
	if err := json.Unmarshal(manRaw, &man); err != nil {
		t.Fatal(err)
	}
	procs := map[string]bool{}
	for _, p := range man.Processes {
		procs[p.ID] = false
	}
	keys := map[string]bool{}
	for _, c := range a.Capabilities {
		keys[c.Key] = true
	}
	for _, c := range a.Capabilities {
		for _, msg := range capability.ValidateCapability(c) {
			t.Errorf("capability %s: %s", c.Key, msg)
		}
		for _, r := range c.Realizations {
			if r.Kind != capability.RealizationProcess || r.ApplicationKey != man.Key {
				continue
			}
			if _, ok := procs[r.ProcessID]; !ok {
				t.Errorf("capability %s: realized by %s, which is not a process of %s", c.Key, r.ProcessID, man.Key)
			}
			procs[r.ProcessID] = true
		}
		for _, k := range c.Requires {
			if !keys[k] {
				t.Errorf("capability %s requires %s, which the file does not define", c.Key, k)
			}
		}
	}
	for id, used := range procs {
		if !used {
			t.Errorf("no capability is realized by %s", id)
		}
	}
	for _, v := range a.ValueStreams {
		for _, msg := range capability.ValidateValueStream(v) {
			t.Errorf("value stream %s: %s", v.Key, msg)
		}
		for _, s := range v.Stages {
			for _, k := range s.Capabilities {
				if !keys[k] {
					t.Errorf("value stream %s, stage %s: capability %s is not defined", v.Key, s.Key, k)
				}
			}
		}
	}
}

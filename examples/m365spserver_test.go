package examples

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestSharePointServerTeamSiteIsOnlyInTheVariant: the on-premises team site lives in
// its own folder and in no document the package ships to every installation, because a
// process naming ucc_sharepoint_onprem would park forever where no farm exists.
func TestSharePointServerTeamSiteIsOnlyInTheVariant(t *testing.T) {
	for _, f := range []string{"m365-dienste/atlas.json", "m365-dienste/katalog.json"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "m365-spserver") || strings.Contains(string(raw), "ucc_sharepoint_onprem") {
			t.Errorf("%s names the on-premises team site", f)
		}
	}
}

// TestSharePointServerProductNamesWhatItShips holds the fragment to its files and the
// process's data objects to the class it proposes, ADR-0058's rule for a model that
// does not sit in the package's own information model.
func TestSharePointServerProductNamesWhatItShips(t *testing.T) {
	raw, err := os.ReadFile("m365-dienste/sharepoint-server/produkt.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Process struct{ Path string }
		Forms   []struct{ ID, Path string }
		Product struct {
			HomeCatalog string
			Actions     []struct {
				Key, Message, Form string
			}
		}
		Class struct {
			Name      string
			Lifecycle struct {
				States []struct{ Name string }
			}
		}
		Store struct{ Name, Class, Worker string }
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Product.HomeCatalog != "cat-m365-onprem" {
		t.Errorf("homeCatalog = %q", doc.Product.HomeCatalog)
	}
	src, err := os.ReadFile("m365-dienste/" + doc.Process.Path)
	if err != nil {
		t.Fatal(err)
	}
	formIDs := map[string]bool{}
	for _, f := range doc.Forms {
		if _, err := os.Stat("m365-dienste/" + f.Path); err != nil {
			t.Errorf("form %s: %v", f.ID, err)
		}
		formIDs[f.ID] = true
	}
	for _, a := range doc.Product.Actions {
		if !strings.Contains(string(src), `name="`+a.Message+`"`) {
			t.Errorf("no message %s in the process", a.Message)
		}
		if a.Form != "" && !formIDs[a.Form] {
			t.Errorf("action %s names form %s, which the fragment does not ship", a.Key, a.Form)
		}
	}
	if doc.Store.Class != doc.Class.Name || doc.Store.Worker != "ucc_sharepoint_onprem" {
		t.Errorf("store = %+v", doc.Store)
	}
	var d struct {
		Process struct {
			Refs []struct {
				ID    string `xml:"id,attr"`
				Ref   string `xml:"dataObjectRef,attr"`
				State struct {
					Name string `xml:"name,attr"`
				} `xml:"dataState"`
			} `xml:"dataObjectReference"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(src, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Process.Refs) == 0 {
		t.Fatal("no data object references")
	}
	var states []string
	for _, s := range doc.Class.Lifecycle.States {
		states = append(states, s.Name)
	}
	for _, r := range d.Process.Refs {
		if r.Ref == "do_site" && !slices.Contains(states, r.State.Name) {
			t.Errorf("%s writes the state %q, which %s does not have", r.ID, r.State.Name, doc.Class.Name)
		}
	}
}

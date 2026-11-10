package examples

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/formavailability"
	"github.com/pblumer/atlas/expr"
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

// spsNode is one element of the team site's process, as far as these tests read it.
type spsNode struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Source  string `xml:"sourceRef,attr"`
	Target  string `xml:"targetRef,attr"`
	Default string `xml:"default,attr"`
	Inner   string `xml:",innerxml"`
}

func spsNodes(t *testing.T) map[string]spsNode {
	t.Helper()
	raw, err := os.ReadFile("m365-dienste/sharepoint-server/sharepoint-server-site.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Process struct {
			Nodes []spsNode `xml:",any"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	out := map[string]spsNode{}
	for _, n := range d.Process.Nodes {
		out[n.ID] = n
	}
	return out
}

// spsAttr reads one attribute of the first element named el inside n.
func spsAttr(t *testing.T, n spsNode, el, attr string) string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader("<x>" + n.Inner + "</x>"))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s has no %s with %s", n.ID, el, attr)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == el {
			for _, a := range se.Attr {
				if a.Name.Local == attr {
					return a.Value
				}
			}
		}
	}
}

// TestSharePointServerTeamSiteChecksTheAddressFirst: between the name rule and
// create-site the process reads the address with get-site, and goes on only when nobody
// has it or when the site there is this very position's (a redelivery). Anything else is
// the refusal, with a reason that says the address is taken; the error boundary on
// create-site stays for the race between the check and the creation.
func TestSharePointServerTeamSiteChecksTheAddressFirst(t *testing.T) {
	nodes := spsNodes(t)
	flow := func(id string) spsNode {
		n, ok := nodes[id]
		if !ok || n.XMLName.Local != "sequenceFlow" {
			t.Fatalf("no sequence flow %s", id)
		}
		return n
	}
	if f := flow("f_gw_ja"); f.Source != "gw_name" || f.Target != "st_site_pruefen" {
		t.Fatalf("f_gw_ja = %s -> %s, want gw_name -> st_site_pruefen", f.Source, f.Target)
	}
	check := nodes["st_site_pruefen"]
	if spsAttr(t, check, "sharepointServerConnector", "operation") != "get-site" ||
		spsAttr(t, check, "sharepointServerConnector", "connector") != "ucc_sharepoint_onprem" ||
		spsAttr(t, check, "sharepointServerConnector", "resultVariable") != "siteAlt" ||
		!strings.Contains(spsAttr(t, check, "sharepointServerConnector", "parameters"), `"Site": "=regel.alias"`) {
		t.Fatalf("st_site_pruefen is not get-site on regel.alias into siteAlt: %s", check.Inner)
	}
	gw := nodes["gw_frei"]
	if gw.XMLName.Local != "exclusiveGateway" {
		t.Fatal("no gateway gw_frei")
	}
	var onward spsNode
	for _, n := range nodes {
		if n.XMLName.Local == "sequenceFlow" && n.Source == "st_site_pruefen" && n.Target != "gw_frei" {
			t.Errorf("st_site_pruefen leads to %s", n.Target)
		}
		if n.XMLName.Local == "sequenceFlow" && n.Source == "gw_frei" {
			switch n.Target {
			case "st_anlegen":
				onward = n
			case "st_vergeben":
				if gw.Default != n.ID {
					t.Errorf("the refusal %s is not the gateway's default (fail-closed)", n.ID)
				}
			default:
				t.Errorf("gw_frei leads to %s", n.Target)
			}
		}
	}
	cond := strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(
		strings.TrimSpace(onward.Inner), "<conditionExpression>"), "</conditionExpression>")), "=")
	if cond == "" {
		t.Fatal("gw_frei has no conditional flow to st_anlegen")
	}
	// The position create-site writes into the site is the one the gateway compares.
	anlegen := spsAttr(t, nodes["st_anlegen"], "sharepointServerConnector", "parameters")
	if !strings.Contains(anlegen, `"atlas.position": "=string(orderId) + \":\" + string(if positionId = null then itemId else positionId)"`) {
		t.Fatalf("st_anlegen writes another position than the test assumes: %s", anlegen)
	}
	reason := ""
	for _, part := range strings.Split(nodes["st_vergeben"].Inner, "<zeebe:input ") {
		if strings.Contains(part, `target="reason"`) {
			reason = strings.TrimPrefix(strings.SplitN(strings.SplitN(part, `source="`, 2)[1], `"`, 2)[0], "=")
		}
	}
	reason = strings.ReplaceAll(reason, "&quot;", `"`)
	cond = strings.ReplaceAll(cond, "&quot;", `"`)
	goOn, err := expr.CompileAuto(cond)
	if err != nil {
		t.Fatalf("gw_frei condition: %v", err)
	}
	why, err := expr.CompileAuto(reason)
	if err != nil {
		t.Fatalf("st_vergeben reason: %v", err)
	}
	site := func(props string) expr.Value {
		v, err := expr.ParseJSON(`{"Url": "http://sp01/sites/alpha", "Owner": "CONTOSO\\a", "Properties": ` + props + `}`)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		name   string
		site   expr.Value
		onward bool
	}{
		{"no site at the address", expr.FromStored(expr.KindNull, false, ""), true},
		{"this position's site (a redelivery)", site(`{"atlas.position": "o-1:p-7", "atlas.produkt": "m365-spserver-site"}`), true},
		{"another position's site", site(`{"atlas.position": "o-2:p-1", "atlas.produkt": "m365-spserver-site"}`), false},
		{"a site Atlas did not make", site(`{}`), false},
	} {
		vars := map[string]expr.Value{
			"siteAlt": tc.site, "orderId": expr.String("o-1"), "positionId": expr.String("p-7"), "itemId": expr.String("i-3"),
		}
		got, err := goOn.Eval(vars)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if expr.IsTrue(got) != tc.onward {
			t.Errorf("%s: goes on to create-site = %v, want %v", tc.name, expr.IsTrue(got), tc.onward)
		}
		if tc.onward {
			continue
		}
		r, err := why.Eval(vars)
		if err != nil {
			t.Fatalf("%s: reason: %v", tc.name, err)
		}
		if _, _, text := expr.Classify(r); text != "Die Adresse ist bereits vergeben." {
			t.Errorf("%s: reason = %q", tc.name, text)
		}
	}
	// Reached from create-site's boundary events, the refusal keeps its broader reason.
	r, err := why.Eval(map[string]expr.Value{"siteAlt": expr.FromStored(expr.KindNull, false, ""), "orderId": expr.String("o-1"), "positionId": expr.String("p-7")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, text := expr.Classify(r); !strings.Contains(text, "Konto") {
		t.Errorf("boundary reason = %q, want the one naming the account too", text)
	}
	for _, b := range []string{"b_anl_a", "b_anl_b"} {
		if !strings.Contains(nodes[b].Inner, "errorEventDefinition") {
			t.Errorf("%s is gone; it is the last line against a race", b)
		}
	}
}

// TestSharePointServerOrderFormChecksTheURLLive: the order form asks the farm's Worker
// whether the URL is free while the person types, as the online form asks SharePoint
// Online (ADR-0519). The installer copies this form into the on-premises variant as it
// is, so it names the variant's worker itself.
func TestSharePointServerOrderFormChecksTheURLLive(t *testing.T) {
	raw, err := os.ReadFile("m365-dienste/sharepoint-server/m365-spserver-site-bestellung.form.json")
	if err != nil {
		t.Fatal(err)
	}
	form, err := formavailability.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("url")
	if !ok {
		t.Fatal("the url field has no availability check")
	}
	if len(f.Source.Checks) != 1 {
		t.Fatalf("checks = %+v", f.Source.Checks)
	}
	if ck := f.Source.Checks[0]; ck.WorkerType != "sharepointserver" || ck.Worker != "ucc_sharepoint_onprem" || ck.Resource != "sites" {
		t.Errorf("check = %+v, want sharepointserver/sites on ucc_sharepoint_onprem", ck)
	}
}

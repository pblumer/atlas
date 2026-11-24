package examples

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html"
	"os"
	"regexp"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// TestM365HREntryNamesTakeThePrefix evaluates the naming rule of hr-eintritt.bpmn, which
// lives in the Entra Worker's expressions rather than in M365Namensregel: without a prefix
// the account is vorname.nachname@<MAIL_DOMAIN> as the README documents, and with
// NAME_PREFIX (a shared tenant, deploy/opentofu/m365-devenv) the UPN, the alias, the
// display name and the search for taken names all start with it, so the environment's
// cleanup and its directory mirroring find the account. The department is the OE unless
// DEPARTMENT is set: in a shared tenant the environment id, the department its directory
// mirroring keeps.
func TestM365HREntryNamesTakeThePrefix(t *testing.T) {
	b, err := os.ReadFile("m365-dienste/hr-eintritt.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	filter := html.UnescapeString(regexp.MustCompile(`operation="list-users" filter="=([^"]*)"`).FindStringSubmatch(src)[1])
	var attrs map[string]any
	raw := html.UnescapeString(regexp.MustCompile(`operation="create-user" attributes="([^"]*)"`).FindStringSubmatch(src)[1])
	if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
		t.Fatal(err)
	}
	eval := func(name, src string, vars map[string]expr.Value) string {
		t.Helper()
		c, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v, err := c.Eval(vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		j, ok := expr.ToJSON(v)
		var s string
		if !ok || json.Unmarshal([]byte(j), &s) != nil {
			t.Fatalf("%s: %v is not a string", name, v)
		}
		return s
	}
	field := func(name string) string { return attrs[name].(string)[1:] }

	cases := []struct {
		name, prefix, department, taken                                string
		wantFilter, wantUPN, wantNickname, wantDisplay, wantDepartment string
	}{
		{name: "no prefix", wantFilter: "startswith(userPrincipalName,'claudia.bruenner')",
			wantUPN: "claudia.bruenner@contoso.ch", wantNickname: "claudia.bruenner", wantDisplay: "Claudia Brünner", wantDepartment: "GL"},
		{name: "no prefix, name taken", taken: "Claudia.Bruenner@Contoso.ch",
			wantFilter: "startswith(userPrincipalName,'claudia.bruenner')",
			wantUPN:    "claudia.bruenner2@contoso.ch", wantNickname: "claudia.bruenner2", wantDisplay: "Claudia Brünner",
			wantDepartment: "GL"},
		{name: "shared tenant", prefix: "atlas-test-ab12-", department: "atlas-test-ab12",
			wantFilter: "startswith(userPrincipalName,'atlas-test-ab12-claudia.bruenner')",
			wantUPN:    "atlas-test-ab12-claudia.bruenner@contoso.ch", wantNickname: "atlas-test-ab12-claudia.bruenner",
			wantDisplay: "atlas-test-ab12-Claudia Brünner", wantDepartment: "atlas-test-ab12"},
		{name: "shared tenant, name taken", prefix: "atlas-test-ab12-", department: "atlas-test-ab12", taken: "atlas-test-ab12-claudia.bruenner@contoso.ch",
			wantFilter: "startswith(userPrincipalName,'atlas-test-ab12-claudia.bruenner')",
			wantUPN:    "atlas-test-ab12-claudia.bruenner2@contoso.ch", wantNickname: "atlas-test-ab12-claudia.bruenner2",
			wantDisplay: "atlas-test-ab12-Claudia Brünner", wantDepartment: "atlas-test-ab12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taken := []any{}
			if tc.taken != "" {
				taken = append(taken, map[string]any{"userPrincipalName": tc.taken})
			}
			vars := map[string]expr.Value{
				"vorname": expr.FromJSON("Claudia"), "nachname": expr.FromJSON("Brünner"), "oe": expr.FromJSON("GL"),
				"belegteUpns": expr.FromJSON(taken),
				"application": expr.FromJSON(map[string]any{"NAME_PREFIX": tc.prefix, "DEPARTMENT": tc.department, "MAIL_DOMAIN": "contoso.ch"}),
			}
			for _, c := range []struct{ name, src, want string }{
				{"filter", filter, tc.wantFilter},
				{"userPrincipalName", field("userPrincipalName"), tc.wantUPN},
				{"mailNickname", field("mailNickname"), tc.wantNickname},
				{"displayName", field("displayName"), tc.wantDisplay},
				{"department", field("department"), tc.wantDepartment},
			} {
				if got := eval(c.name, c.src, vars); got != c.want {
					t.Errorf("%s = %q, want %q", c.name, got, c.want)
				}
			}
		})
	}
}

// TestM365HRLeaverReturnsOrbithekAccounts pins the leaver's Orbithek branch of
// hr-eintritt.bpmn: every Orbithek product is returned by recipient (ADR-0521), so the
// process needs no order or position it never placed; each return has a refusal boundary
// that hands it to the group Admin; and the end of the leaver waits for that branch as
// for the other two. The model must still compile.
func TestM365HRLeaverReturnsOrbithekAccounts(t *testing.T) {
	b, err := os.ReadFile("m365-dienste/hr-eintritt.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Parse(1, 1, bytes.NewReader(b)); err != nil {
		t.Fatalf("compile: %v", err)
	}
	var doc struct {
		Process struct {
			SendTasks []struct {
				ID   string `xml:"id,attr"`
				Shop struct {
					Mode      string `xml:"mode,attr"`
					Product   string `xml:"product,attr"`
					Action    string `xml:"action,attr"`
					Recipient string `xml:"recipient,attr"`
				} `xml:"extensionElements>shopTask"`
			} `xml:"sendTask"`
			Boundaries []struct {
				AttachedTo string `xml:"attachedToRef,attr"`
				Outgoing   string `xml:"outgoing"`
				Error      struct {
					Ref string `xml:"errorRef,attr"`
				} `xml:"errorEventDefinition"`
			} `xml:"boundaryEvent"`
			UserTasks []struct {
				ID     string `xml:"id,attr"`
				Groups struct {
					Candidates string `xml:"candidateGroups,attr"`
				} `xml:"extensionElements>assignmentDefinition"`
			} `xml:"userTask"`
			Flows []struct {
				ID     string `xml:"id,attr"`
				Target string `xml:"targetRef,attr"`
			} `xml:"sequenceFlow"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	pr := doc.Process
	target := map[string]string{}
	for _, f := range pr.Flows {
		target[f.ID] = f.Target
	}
	group := map[string]string{}
	for _, u := range pr.UserTasks {
		group[u.ID] = u.Groups.Candidates
	}
	for _, product := range []string{"orbithek-konto-wiki-dev-admin-ch", "orbithek-konto-wiki-blumer-cloud"} {
		t.Run(product, func(t *testing.T) {
			task := ""
			for _, st := range pr.SendTasks {
				if st.Shop.Product == product {
					task = st.ID
					if st.Shop.Mode != "command" || st.Shop.Action != "deprovision" || st.Shop.Recipient != "=atlasKontoId" {
						t.Errorf("%s: mode=%q action=%q recipient=%q, want command, deprovision, =atlasKontoId",
							st.ID, st.Shop.Mode, st.Shop.Action, st.Shop.Recipient)
					}
				}
			}
			if task == "" {
				t.Fatalf("no shop task returns %s", product)
			}
			found := false
			for _, be := range pr.Boundaries {
				if be.AttachedTo != task {
					continue
				}
				found = true
				if be.Error.Ref != "err_rueckgabe" {
					t.Errorf("boundary on %s catches %q, want err_rueckgabe", task, be.Error.Ref)
				}
				if g := group[target[be.Outgoing]]; g != "Admin" {
					t.Errorf("a refused return of %s goes to %q, want a task for the group Admin", product, g)
				}
			}
			if !found {
				t.Errorf("%s has no refusal boundary", task)
			}
		})
	}
	joins := 0
	for _, f := range pr.Flows {
		if f.Target == "gw_rende" {
			joins++
		}
	}
	if joins != 3 {
		t.Errorf("gw_rende joins %d branches, want 3 (package, AD admin account, Orbithek)", joins)
	}
}

package examples

import (
	"encoding/json"
	"html"
	"os"
	"regexp"
	"testing"

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

package ad_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/model"
)

// The inline entry, select and foundVariable (ADR-0166, amended 2026-11-03), through
// the whole path a model takes: compiled from BPMN, resolved at the handoff, performed.

// adTaskModel compiles a model whose one AD task carries the given attributes and is
// followed by a parked service task, so the instance — and its variables — outlive the
// AD job for the test to read.
func adTaskModel(t *testing.T, attrs string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	src := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="directory" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>
      <atlas:adConnector url="` + adURL + `" ` + attrs + `/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:serviceTask id="wait"><bpmn:extensionElements><zeebe:taskDefinition type="wait"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="wait"/>
    <bpmn:sequenceFlow id="f3" sourceRef="wait" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(adDefKey, 1, strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp, compiler.AdJobTypeIndex
}

func str(name, v string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarString, Text: v}
}

// A create takes its entry from the template: strings as one value, a list as several,
// a number and a boolean as their LDAP text, and a null — written, or an expression over
// a variable nobody set — left out, because an add cannot carry an attribute without a
// value. The object class is still supplied when the template does not name one.
func TestAdCreateUserFromInlineAttributes(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adTaskModel(t, `operation="create-user" dn="=&quot;CN=&quot; + vorname + &quot; &quot; + nachname + &quot;,OU=users,DC=x&quot;"
	    attributes="{&quot;sAMAccountName&quot;: &quot;=konto&quot;, &quot;givenName&quot;: &quot;=vorname&quot;, &quot;sn&quot;: &quot;=nachname&quot;,
	      &quot;displayName&quot;: &quot;=vorname + \&quot; \&quot; + nachname&quot;, &quot;proxyAddresses&quot;: [&quot;=\&quot;SMTP:\&quot; + mail&quot;, &quot;smtp:alt@x.ch&quot;, null],
	      &quot;userAccountControl&quot;: 514, &quot;msExchHideFromAddressLists&quot;: true, &quot;description&quot;: null, &quot;telephoneNumber&quot;: &quot;=telefon&quot;,
	      &quot;otherMailbox&quot;: []}"`)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
		str("vorname", "Ida"), str("nachname", "Muster"), str("konto", "imuster"), str("mail", "ida@x.ch"))

	if conn.addDN != "CN=Ida Muster,OU=users,DC=x" {
		t.Fatalf("add dn = %q", conn.addDN)
	}
	want := map[string][]string{
		"sAMAccountName":             {"imuster"},
		"givenName":                  {"Ida"},
		"sn":                         {"Muster"},
		"displayName":                {"Ida Muster"},
		"proxyAddresses":             {"SMTP:ida@x.ch", "smtp:alt@x.ch"},
		"userAccountControl":         {"514"},
		"msExchHideFromAddressLists": {"TRUE"},
		"objectClass":                {"top", "person", "organizationalPerson", "user"},
	}
	if !reflect.DeepEqual(conn.addAt, want) {
		t.Errorf("entry = %v\nwant    %v", conn.addAt, want)
	}
}

// On update-attributes a null clears the attribute — an LDAP replace with no values —
// because "this person has no phone number any more" is a change the update must be able
// to say. Everything named is replaced, in a stable order.
func TestAdUpdateAttributesInlineNullClears(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adTaskModel(t, `operation="update-attributes" dn="cn=Ida,dc=x"
	    attributes="{&quot;title&quot;: &quot;=titel&quot;, &quot;telephoneNumber&quot;: null, &quot;mobile&quot;: &quot;=handy&quot;, &quot;employeeNumber&quot;: 4711}"`)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log, str("titel", "Leiterin"))

	got := map[string][]string{}
	var order []string
	for _, m := range conn.mods {
		got[m.Attr] = m.Vals
		order = append(order, m.Attr)
	}
	want := map[string][]string{
		"employeeNumber":  {"4711"},
		"mobile":          {},
		"telephoneNumber": {},
		"title":           {"Leiterin"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mods = %v, want %v", got, want)
	}
	if strings.Join(order, ",") != "employeeNumber,mobile,telephoneNumber,title" {
		t.Errorf("order = %v, want sorted", order)
	}
}

// A null clearing an attribute survives the trip to a worker: the job is JSON on the
// wire, and an empty list must not arrive as "no such attribute in the entry".
func TestAdClearingSurvivesTheWire(t *testing.T) {
	j := ad.Job{Operation: "update-attributes", Attributes: map[string][]string{"mobile": {}, "title": {"x"}}}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var back ad.Job
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	vals, ok := back.Attributes["mobile"]
	if !ok || vals == nil || len(vals) != 0 {
		t.Errorf("after the wire mobile = %#v (present %v), want an empty list that clears it (%s)", vals, ok, raw)
	}
}

// A template the compiler cannot see through — an =expression that produces an object
// — is refused when it is evaluated, with the attribute named, and parks the job.
func TestAdInlineAttributeThatEvaluatesToAnObjectIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, tmpl string }{
		{"object", `{&quot;manager&quot;: &quot;=chef&quot;}`},
		{"object in a list", `{&quot;seeAlso&quot;: [&quot;=chef&quot;]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, store := openStore(t)
			cp, jobType := adTaskModel(t, `operation="create-user" dn="cn=a,dc=x" attributes="`+tc.tmpl+`"`)
			conn := &fakeConn{}
			drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log,
				model.VariableValue{Name: "chef", Kind: model.VarJSON, Text: `{"dn":"cn=b,dc=x"}`})
			if conn.addDN != "" {
				t.Errorf("an entry was added: %v", conn.addAt)
			}
			if mustActiveProcs(t, store) != 1 {
				t.Error("want the job parked on an incident")
			}
		})
	}
}

// A create whose template resolves to nothing at all is the same mistake an empty
// entry variable is, and is refused the same way.
func TestAdCreateWithEveryInlineValueNullIsRefused(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adTaskModel(t, `operation="create-user" dn="cn=a,dc=x" attributes="{&quot;sAMAccountName&quot;: &quot;=konto&quot;}"`)
	conn := &fakeConn{}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)
	if conn.addDN != "" {
		t.Errorf("an entry was added: %v", conn.addAt)
	}
	if mustActiveProcs(t, store) != 1 {
		t.Error("want the job parked on an incident")
	}
}

// select reaches the directory as the search's attribute list, and foundVariable is
// written beside the result.
func TestAdSearchWithSelectAndFoundVariable(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adTaskModel(t, `operation="search" baseDN="ou=users,dc=x" filter="(sAMAccountName=imuster)" select="cn, mail"
	    resultVariable="treffer" foundVariable="gefunden"`)
	conn := &fakeConn{searchRes: []ad.Entry{{DN: "cn=Ida Muster,ou=users,dc=x", Attributes: map[string][]string{"cn": {"Ida Muster"}}}}}
	drive(t, cp, jobType, &fakeDialer{conn: conn}, noSecret, store, log)

	if conn.searchReq == nil || !reflect.DeepEqual(conn.searchReq.Attributes, []string{"cn", "mail"}) {
		t.Fatalf("search request = %+v, want the selected attributes asked for", conn.searchReq)
	}
	vars := instanceVars(t, store)
	found, ok := boolVar(t, store, "gefunden")
	if !ok || !found {
		t.Errorf("gefunden = %v (set %v), want true", found, ok)
	}
	if !strings.Contains(vars["treffer"], `"dn":"cn=Ida Muster,ou=users,dc=x"`) {
		t.Errorf("treffer = %s, want the result beside the flag", vars["treffer"])
	}
}

// A foundVariable alone is a whole search: the flag is written and nothing else, so a
// name check leaves no person's entry behind in the instance.
func TestAdSearchWithOnlyAFoundVariableWritesOnlyTheFlag(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := adTaskModel(t, `operation="search" baseDN="ou=users,dc=x" filter="(sAMAccountName=imuster)" foundVariable="vergeben"`)
	drive(t, cp, jobType, &fakeDialer{conn: &fakeConn{}}, noSecret, store, log)

	found, ok := boolVar(t, store, "vergeben")
	if !ok || found {
		t.Errorf("vergeben = %v (set %v), want false", found, ok)
	}
	vars := instanceVars(t, store)
	if len(vars) != 1 {
		t.Errorf("variables = %v, want only the flag", vars)
	}
}

// boolVar reads one boolean variable of the single live instance.
func boolVar(t *testing.T, s interface {
	ActiveProcessInstances(func(uint64, *model.ProcessInstanceValue) error) error
	VariablesOfScope(uint64, func(*model.VariableValue) error) error
}, name string) (val, ok bool) {
	t.Helper()
	var keys []uint64
	_ = s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	})
	for _, k := range keys {
		_ = s.VariablesOfScope(k, func(v *model.VariableValue) error {
			if v.Name == name {
				if v.Kind != model.VarBool {
					t.Errorf("%s has kind %v, want a boolean", name, v.Kind)
				}
				val, ok = v.Bool, true
			}
			return nil
		})
	}
	return val, ok
}

// The mock projects a search's entries onto the selection the way a directory does:
// only the named attributes, matched without regard to case, under the entry's own
// spelling; "*" is everything and "1.1" nothing.
func TestMockSearchAppliesTheSelection(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN, Attributes: map[string][]string{
		"sAMAccountName": {"arno"}, "mail": {"arno@example.com"}, "givenName": {"Arno"},
	}})
	search := func(sel ...string) map[string]any {
		out := run(t, d, ad.Job{URL: mockTLSURL, Operation: "search", BaseDN: usersDN, Filter: "(sAMAccountName=arno)",
			Select: sel, ResultVariable: "r", FoundVariable: "f"})
		if out["f"] != true {
			t.Errorf("select %v: found = %v, want true", sel, out["f"])
		}
		entries := out["r"].(map[string]any)["entries"].([]any)
		if len(entries) != 1 {
			t.Fatalf("select %v: entries = %v", sel, entries)
		}
		return entries[0].(map[string]any)["attributes"].(map[string]any)
	}
	if got := search("MAIL", "cn"); len(got) != 1 || got["mail"] == nil {
		t.Errorf("select MAIL,cn = %v, want mail alone under the entry's own spelling", got)
	}
	if got := search("*"); len(got) < 3 {
		t.Errorf("select * = %v, want every attribute", got)
	}
	if got := search(); len(got) < 3 {
		t.Errorf("no selection = %v, want every attribute, as before", got)
	}
	if got := search("1.1"); len(got) != 0 {
		t.Errorf("select 1.1 = %v, want no attributes", got)
	}
}

// The mock clears an attribute on an inline null the way AD does, and leaves a create's
// null out of the entry.
func TestMockInlineNullOnCreateAndUpdate(t *testing.T) {
	d := ad.NewMockDirectory()
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "create-user", DN: arnoDN, Attributes: map[string][]string{
		"sAMAccountName": {"arno"}, "mobile": {"079"},
	}})
	run(t, d, ad.Job{URL: mockTLSURL, Operation: "update-attributes", DN: arnoDN, Attributes: map[string][]string{
		"mobile": {}, "title": {"Leiter"},
	}})
	e := entry(t, d, arnoDN)
	if _, has := e.Attributes["mobile"]; has {
		t.Errorf("mobile = %v, want it cleared", e.Attributes["mobile"])
	}
	if e.Attributes["title"][0] != "Leiter" {
		t.Errorf("title = %v", e.Attributes["title"])
	}
}

// The mock applies AD's bitwise matching rules, so a model can ask for "only enabled
// accounts" — (!(userAccountControl:1.2.840.113556.1.4.803:=2)) — the way it asks AD,
// and decodes RFC 4515 escapes, so a DN escaped into a filter finds the entry it came
// from.
func TestMockFilterBitwiseRulesAndEscapes(t *testing.T) {
	const meierDN = `cn=Meier\, Arno (IT),ou=users,dc=example,dc=com`
	d := ad.NewMockDirectory(
		ad.Entry{DN: arnoDN, Attributes: map[string][]string{"sAMAccountName": {"arno"}, "userAccountControl": {"512"}}},
		ad.Entry{DN: "cn=Bea,ou=users,dc=example,dc=com", Attributes: map[string][]string{"sAMAccountName": {"bea"}, "userAccountControl": {"514"}}},
		ad.Entry{DN: "cn=Gruppe,ou=groups,dc=example,dc=com", Attributes: map[string][]string{"cn": {"Gruppe"}, "member": {meierDN}}},
		ad.Entry{DN: "cn=Stern,ou=groups,dc=example,dc=com", Attributes: map[string][]string{"cn": {"a*b"}}},
	)
	count := func(filter string) int {
		out := run(t, d, ad.Job{URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Filter: filter, ResultVariable: "r"})
		return out["r"].(map[string]any)["count"].(int)
	}
	for _, tc := range []struct {
		filter string
		want   int
	}{
		{"(&(sAMAccountName=*)(!(userAccountControl:1.2.840.113556.1.4.803:=2)))", 1},
		{"(userAccountControl:1.2.840.113556.1.4.803:=2)", 1},
		{"(userAccountControl:1.2.840.113556.1.4.803:=514)", 1},
		{"(userAccountControl:1.2.840.113556.1.4.804:=6)", 1},
		{"(userAccountControl:1.2.840.113556.1.4.804:=512)", 2},
		{`(member=cn=Meier\5c, Arno \28IT\29,ou=users,dc=example,dc=com)`, 1},
		{`(member=cn=meier\5c, arno*)`, 1},
		{`(cn=a\2ab)`, 1},
		{`(cn=a\2a)`, 0},
	} {
		if got := count(tc.filter); got != tc.want {
			t.Errorf("%s: %d entries, want %d", tc.filter, got, tc.want)
		}
	}
	for _, bad := range []string{
		"(userAccountControl:1.2.3:=2)",
		"(userAccountControl:1.2.840.113556.1.4.803:=zwei)",
		`(cn=a\2)`,
		`(cn=a\zz)`,
	} {
		err := runErr(t, d, ad.Job{URL: mockTLSURL, Operation: "search", BaseDN: baseDN, Filter: bad, ResultVariable: "r"})
		if !strings.Contains(err.Error(), "filter") {
			t.Errorf("%s: error = %v, want it to name the filter", bad, err)
		}
	}
}

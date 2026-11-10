package compiler

import (
	"strings"
	"testing"
)

// A delta query may name a keep (ADR-draft-entra-delta-keep): the filter the Entra
// Worker applies to every page before it answers. It compiles to the interned text the
// job carries; a task without one carries none.
func TestEntraKeepCompiles(t *testing.T) {
	cp, d := entraDetail(t, `connector="c" operation="delta-users" resultVariable="konten"
		select="id,displayName,department" keep="department eq 'atlas-test-58o4' or startswith(displayName,'atlas-test-58o4')"`)
	if got := cp.Intern(d.EntraKeep); got != "department eq 'atlas-test-58o4' or startswith(displayName,'atlas-test-58o4')" {
		t.Errorf("keep = %q", got)
	}
	// Fields are matched against the select case-insensitively, and id needs no select
	// entry: Graph returns it on every object.
	cp, d = entraDetail(t, `connector="c" operation="delta-groups" resultVariable="g"
		select="ID,DisplayName" keep="startswith(displayname,'x') or id eq 'y'"`)
	if cp.Intern(d.EntraKeep) == "" {
		t.Error("a keep on delta-groups lost on the way")
	}
	cp, d = entraDetail(t, `connector="c" operation="delta-users" resultVariable="r" select="id"`)
	if d.EntraKeep != -1 || cp.Intern(d.EntraKeep) != "" {
		t.Errorf("a delta without keep carries %d / %q", d.EntraKeep, cp.Intern(d.EntraKeep))
	}
}

// What a keep is refused for at deploy, each because it could not work at runtime: on
// the wrong operation it would be ignored, as FEEL it could not be checked, and a field
// outside the select is absent from every page, which would withhold every object.
func TestEntraKeepRefusals(t *testing.T) {
	cases := map[string]struct{ attrs, want string }{
		"on a listing": {
			`connector="c" operation="list-users" resultVariable="r" select="id,department" keep="department eq 'x'"`,
			"not a change-tracking query",
		},
		"on a write": {
			`connector="c" operation="disable" userId="u" keep="department eq 'x'"`,
			"not a change-tracking query",
		},
		"as FEEL": {
			`connector="c" operation="delta-users" resultVariable="r" select="id,department" keep="=abteilung"`,
			"FEEL expression",
		},
		"unknown operator": {
			`connector="c" operation="delta-users" resultVariable="r" select="id,department" keep="department ne 'x'"`,
			`"ne"`,
		},
		"malformed": {
			`connector="c" operation="delta-users" resultVariable="r" select="id,department" keep="department eq"`,
			"single-quoted literal",
		},
		"no select": {
			`connector="c" operation="delta-users" resultVariable="r" keep="department eq 'x'"`,
			"without a select",
		},
		"field outside the select": {
			`connector="c" operation="delta-users" resultVariable="r" select="id,displayName" keep="department eq 'x'"`,
			`reads "department", which is not in its select`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

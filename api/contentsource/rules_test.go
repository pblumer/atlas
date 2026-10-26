package contentsource

import (
	"strings"
	"testing"
)

// The rules of § 8 and § 4, one row each. They are pure functions of the facts,
// so every row of the record's table is a row here, with the fact set exactly
// as the record names it — and every fact an adapter could not establish is a
// row of its own, because "counts as present" is the rule most easily lost.

func viewer(spaces ...string) Standing {
	st := Standing{Scope: ScopeRead, SystemRolesKnown: true, SpacesKnown: true}
	for _, s := range spaces {
		st.Spaces = append(st.Spaces, SpaceGrant{Space: s, Role: RoleViewer})
	}
	return st
}

func livePage() Page {
	return Page{ID: "it_1", Key: "SVC-1", Space: "SVC", ReadRestricted: Absent, Held: Absent, Withdrawn: Absent, Presence: "live"}
}

func TestEverySecurityRowOfTheRecord(t *testing.T) {
	restrictedAncestor := livePage()
	restrictedAncestor.ID, restrictedAncestor.Key, restrictedAncestor.ReadRestricted = "it_0", "SVC-0", Present
	unknownAncestor := livePage()
	unknownAncestor.ID, unknownAncestor.Key, unknownAncestor.ReadRestricted = "it_0", "SVC-0", Unknown

	cases := []struct {
		name      string
		page      func(Page) Page
		ancestors []Page
		standing  Standing
		want      string // "" passes
	}{
		{name: "a live page in a released space passes", page: func(p Page) Page { return p }},
		{name: "a read restriction on the page refuses, even naming the account",
			page: func(p Page) Page { p.ReadRestricted = Present; return p }, want: "read restriction"},
		{name: "a read restriction on an ancestor refuses",
			page: func(p Page) Page { return p }, ancestors: []Page{livePage(), restrictedAncestor}, want: "ancestor SVC-0"},
		{name: "an ancestor whose restriction is unknown refuses",
			page: func(p Page) Page { return p }, ancestors: []Page{unknownAncestor}, want: "ancestor SVC-0"},
		// A restriction set by the governance check is a read restriction like any
		// other: the facts do not distinguish it, so the rule cannot either.
		{name: "an open data protection hold refuses",
			page: func(p Page) Page { p.Held = Present; return p }, want: "data protection"},
		{name: "a trashed page refuses",
			page: func(p Page) Page { p.Withdrawn, p.Presence = Present, "trashed"; return p }, want: "trashed"},
		{name: "a purged or redacted page refuses",
			page: func(p Page) Page { p.Withdrawn, p.Presence = Present, "purged"; return p }, want: "purged"},
		{name: "a space where the account holds no role refuses",
			page: func(p Page) Page { p.Space = "HR"; return p }, want: "not released"},
		{name: "a page whose space is unknown refuses",
			page: func(p Page) Page { p.Space = ""; return p }, want: "which space"},
		{name: "an edit restriction only is no reason: reading stays open",
			page: func(p Page) Page { return p }},
		{name: "a restriction not established refuses",
			page: func(p Page) Page { p.ReadRestricted = Unknown; return p }, want: "did not say whether the page is read-restricted"},
		{name: "a hold not established refuses",
			page: func(p Page) Page { p.Held = Unknown; return p }, want: "did not say whether the page is held"},
		{name: "a presence not established refuses",
			page: func(p Page) Page { p.Withdrawn = Unknown; return p }, want: "did not say whether the page is live"},
		{name: "a space released as editor is no release",
			page: func(p Page) Page { return p },
			standing: Standing{Scope: ScopeRead, SystemRolesKnown: true, SpacesKnown: true,
				Spaces: []SpaceGrant{{Space: "SVC", Role: "viewer"}, {Space: "SVC", Role: "editor", Via: "@pm"}}},
			want: "not released"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := c.standing
			if st.Scope == "" {
				st = viewer("SVC")
			}
			got := CheckPage(c.page(livePage()), c.ancestors, st)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("refused %v, want a pass", got)
				}
				return
			}
			if !strings.Contains(strings.Join(got, "|"), c.want) {
				t.Fatalf("reasons %v do not say %q", got, c.want)
			}
		})
	}
}

// The zero value of a page is a page nothing was established about. It must
// refuse on every fact, or an adapter that forgot a field would pass it.
func TestAPageNothingWasEstablishedAboutRefusesOnEveryFact(t *testing.T) {
	got := CheckPage(Page{}, nil, viewer("SVC"))
	if len(got) != 4 {
		t.Fatalf("reasons = %v, want four: restriction, hold, presence and space", got)
	}
}

func TestTheAccountsStanding(t *testing.T) {
	cases := []struct {
		name string
		st   Standing
		want string
	}{
		{name: "a read-scope viewer passes", st: viewer("SVC", "IT")},
		{name: "a full-scope token refuses", st: func() Standing { s := viewer("SVC"); s.Scope = "full"; return s }(), want: `scope "full"`},
		{name: "an unknown scope refuses", st: func() Standing { s := viewer("SVC"); s.Scope = ""; return s }(), want: "which scope"},
		{name: "a system role refuses", st: func() Standing { s := viewer("SVC"); s.SystemRoles = []string{"erasure"}; return s }(), want: "erasure"},
		{name: "system roles not established refuse", st: func() Standing { s := viewer("SVC"); s.SystemRolesKnown = false; return s }(), want: "system roles"},
		{name: "space roles not established refuse", st: func() Standing { s := viewer(); s.SpacesKnown = false; return s }(), want: "space roles"},
		{name: "an editor grant anywhere refuses", st: func() Standing {
			s := viewer("SVC")
			s.Spaces = append(s.Spaces, SpaceGrant{Space: "OTHER", Role: "editor"})
			return s
		}(), want: `"editor" in space OTHER`},
		{name: "a space administrator through a group refuses", st: func() Standing {
			s := viewer("SVC")
			s.Spaces = append(s.Spaces, SpaceGrant{Space: "SVC", Role: "space-admin", Via: "@admins"})
			return s
		}(), want: "through @admins"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckStanding(c.st)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("refused %v", got)
				}
				return
			}
			if !strings.Contains(strings.Join(got, "|"), c.want) {
				t.Fatalf("reasons %v do not say %q", got, c.want)
			}
		})
	}
}

func TestASpaceIsReleasedOnlyByAViewerGrant(t *testing.T) {
	st := viewer("SVC")
	if !ReleasedFor(st, "SVC") || ReleasedFor(st, "HR") || ReleasedFor(st, "") {
		t.Fatal("release must follow the viewer grant exactly")
	}
	st.SpacesKnown = false
	if ReleasedFor(st, "SVC") {
		t.Fatal("an unknown set of grants releases nothing")
	}
}

func TestFacts(t *testing.T) {
	if !Unknown.Held() || !Present.Held() || Absent.Held() {
		t.Fatal("an unknown fact must count as present")
	}
	if FactOf(true) != Present || FactOf(false) != Absent {
		t.Fatal("FactOf")
	}
}

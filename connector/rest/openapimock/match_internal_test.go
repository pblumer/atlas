package openapimock

import "testing"

func TestCompilePathRefusesUnbalancedBraces(t *testing.T) {
	for _, path := range []string{"/pets/{petId", "/pets/}petId{"} {
		if _, err := compilePath(path); err == nil {
			t.Errorf("compilePath(%q) = nil, want an error", path)
		}
	}
}

func TestMatchSegment(t *testing.T) {
	// {year}-{month}.csv is the case a mux pattern cannot express, and the reason this
	// matcher exists.
	report, err := compilePath("/reports/{year}-{month}.csv")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		path string
		want bool
	}{
		"a whole segment":         {"/reports/2026-09.csv", true},
		"a longer parameter":      {"/reports/2026-2027-09.csv", true},
		"a missing suffix":        {"/reports/2026-09.txt", false},
		"a missing separator":     {"/reports/202609.csv", false},
		"an empty parameter":      {"/reports/-09.csv", false},
		"an empty last parameter": {"/reports/2026-.csv", false},
		"one segment too many":    {"/reports/2026-09.csv/x", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := matches(report, splitPath(tc.path)); got != tc.want {
				t.Errorf("matches(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestAParameterMustMatchSomething(t *testing.T) {
	// /{name}.json against "/": the parameter has nothing to take, and a match here
	// would route an empty name into an operation that expects one.
	tmpl, err := compilePath("/{name}.json")
	if err != nil {
		t.Fatal(err)
	}
	if matches(tmpl, splitPath("/")) {
		t.Error("an empty segment matched a parameter")
	}
	if !matches(tmpl, splitPath("/pets.json")) {
		t.Error("/pets.json did not match")
	}
}

func TestMatchesTheRootPath(t *testing.T) {
	root, err := compilePath("/")
	if err != nil {
		t.Fatal(err)
	}
	if !matches(root, splitPath("/")) {
		t.Error(`"/" does not match itself`)
	}
	if matches(root, splitPath("/pets")) {
		t.Error(`"/" matched /pets`)
	}
}

func TestMatchesAnEmptySegment(t *testing.T) {
	// A document may describe //double, and a caller may send it.
	double, err := compilePath("/a//b")
	if err != nil {
		t.Fatal(err)
	}
	if !matches(double, splitPath("/a//b")) {
		t.Error("/a//b does not match itself")
	}
	if matches(double, splitPath("/a/b")) {
		t.Error("/a//b matched /a/b")
	}
}

func TestSortOperationsIsTotal(t *testing.T) {
	ops := []Operation{
		{Method: "GET", Path: "/a/b/c"},
		{Method: "GET", Path: "/a/{id}/b"},
		{Method: "GET", Path: "/a"},
		{Method: "DELETE", Path: "/a"},
		{Method: "GET", Path: "/a/b"},
		{Method: "GET", Path: "/a/c"},
	}
	for i := range ops {
		segs, err := compilePath(ops[i].Path)
		if err != nil {
			t.Fatal(err)
		}
		ops[i].segments = segs
	}
	sortOperations(ops)
	// Templates of different lengths never compete for one request, so the only
	// requirement across them is that the order is always the same.
	want := []string{"GET /a/b/c", "GET /a/b", "GET /a/c", "GET /a/{id}/b", "DELETE /a", "GET /a"}
	for i, op := range ops {
		if got := op.Method + " " + op.Path; got != want[i] {
			t.Errorf("position %d = %q, want %q (order: %v)", i, got, want[i], ops)
		}
	}
}

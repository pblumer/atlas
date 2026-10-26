package importplan

import (
	"net/url"
	"testing"
)

func TestParseAndOutcome(t *testing.T) {
	d, err := Parse(url.Values{"overwrite": {"process:a, form:b", "process:c"}, "keep": {"*"}, "dryRun": {"true"}})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !d.DryRun || !d.Overwrite["process:a"] || !d.Overwrite["form:b"] || !d.Overwrite["process:c"] || !d.KeepAll {
		t.Fatalf("decisions = %+v", d)
	}
	cases := []struct{ ref, status, want string }{
		{"process:x", New, Created},
		{"process:x", Unchanged, Unchanged},
		{"process:a", Changed, Updated},
		// * in keep covers what overwrite does not name.
		{"process:x", Changed, Kept},
	}
	for _, c := range cases {
		if got := d.Outcome(c.ref, c.status); got != c.want {
			t.Errorf("Outcome(%s, %s) = %s, want %s", c.ref, c.status, got, c.want)
		}
	}

	// Nothing decided: a change stops the import; a name wins over * in the other
	// list.
	none, _ := Parse(url.Values{})
	if got := none.Outcome("process:x", Changed); got != Undecided {
		t.Errorf("undecided outcome = %s", got)
	}
	all, _ := Parse(url.Values{"overwrite": {"*"}, "keep": {"form:b"}})
	if all.Outcome("form:b", Changed) != Kept || all.Outcome("form:c", Changed) != Updated {
		t.Errorf("overwrite=* with keep=form:b = %+v", all)
	}

	for _, q := range []url.Values{
		{"overwrite": {"*"}, "keep": {"*"}},
		{"overwrite": {"process:a"}, "keep": {"process:a"}},
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("Parse(%v) accepted a contradiction", q)
		}
	}
	if Compare(true) != Unchanged || Compare(false) != Changed || !Writes(Created) || !Writes(Updated) || Writes(Kept) {
		t.Error("Compare or Writes")
	}
}

// TestThreeWay: with the version the last import brought remembered, an import
// tells an artifact edited here from one the import has moved on from, and asks
// only when both moved.
func TestThreeWay(t *testing.T) {
	a, b, c := Hash([]byte("a")), Hash([]byte("b")), Hash([]byte("c"))
	cases := []struct {
		name                  string
		exists                bool
		local, incoming, base string
		want                  string
	}{
		{"nothing here", false, "", a, "", New},
		{"the same on both sides", true, a, a, b, Unchanged},
		{"never imported, different", true, a, b, "", Changed},
		{"untouched here, newer in the import", true, a, b, a, Behind},
		{"edited here, the import as before", true, b, a, a, Ahead},
		{"edited here and newer in the import", true, b, c, a, Changed},
	}
	for _, tc := range cases {
		if got := Classify(tc.exists, tc.local, tc.incoming, tc.base); got != tc.want {
			t.Errorf("%s: Classify = %s, want %s", tc.name, got, tc.want)
		}
	}

	// Behind is taken and ahead is kept without a question; a ref names either the
	// other way, and * decides only what both sides changed.
	none := Decisions{}
	if none.Outcome("p", Behind) != Updated || none.Outcome("p", Ahead) != Kept {
		t.Error("the defaults of behind and ahead")
	}
	named := Decisions{Overwrite: map[string]bool{"p": true}, Keep: map[string]bool{"q": true}}
	if named.Outcome("p", Ahead) != Updated || named.Outcome("q", Behind) != Kept {
		t.Error("a named ref does not win over the default")
	}
	all := Decisions{OverwriteAll: true}
	if all.Outcome("p", Ahead) != Kept {
		t.Error("overwrite=* replaced an artifact only this server changed")
	}
	if keepAll := (Decisions{KeepAll: true}); keepAll.Outcome("p", Behind) != Updated {
		t.Error("keep=* held back an artifact only the import changed")
	}

	// Each part is length-prefixed, so a different split is a different hash.
	if Hash([]byte("ab"), []byte("c")) == Hash([]byte("a"), []byte("bc")) {
		t.Error("two splits of the same bytes hash alike")
	}
	if !Asks(Changed) || Asks(Behind) || Asks(Ahead) {
		t.Error("Asks")
	}
}

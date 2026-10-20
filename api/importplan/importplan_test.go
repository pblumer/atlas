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

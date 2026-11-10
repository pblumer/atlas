package entra

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A delta query with a keep (ADR-draft-entra-delta-keep). The scenario every test here
// stands for: a test environment sharing a tenant with real people. Graph's delta
// endpoint cannot be filtered, the job's result is in the log before an output mapping
// could drop anything (I6), so what this worker answers is what the log keeps.

const (
	keepNext  = "https://graph.microsoft.com/v1.0/users/delta?$skiptoken=A"
	keepDelta = "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=Z"
)

func sharedTenantPages() []any {
	return []any{
		map[string]any{"value": []any{
			map[string]any{"id": "t1", "department": "atlas-test-58o4", "displayName": "atlas-test-58o4 Anna Muster", "mail": "anna@test"},
			map[string]any{"id": "r1", "department": "Finance", "displayName": "Real Person One", "mail": "one@contoso.example"},
		}, "@odata.nextLink": keepNext},
		map[string]any{"value": []any{
			map[string]any{"id": "r2", "department": nil, "displayName": "Real Person Two", "mail": "two@contoso.example"},
			// A deletion: always kept, and reduced to what the receiver needs.
			map[string]any{"id": "gone", "displayName": "Real Person Three", "@removed": map[string]any{"reason": "changed"}},
			// A later page's shape: only what changed, no department to decide on.
			map[string]any{"id": "r4", "displayName": "Real Person Four"},
			map[string]any{"id": "t2", "department": "ATLAS-TEST-58O4", "displayName": "atlas-test-58o4 Beat Beispiel"},
		}, "@odata.deltaLink": keepDelta},
	}
}

func TestDeltaKeepFiltersEveryPageBeforeAnswering(t *testing.T) {
	c := &pagingClient{pages: sharedTenantPages()}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten",
		Select: "id,displayName,mail,department", Keep: "department eq 'atlas-test-58o4'",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids, link := deltaResult(t, got["konten"])
	// Both pages filtered, in order, the case-insensitive match included; the removed
	// object kept.
	if strings.Join(ids, ",") != "t1,gone,t2" {
		t.Errorf("kept = %v, want t1,gone,t2", ids)
	}
	// The cursor is Graph's, untouched: the next run reads the same change stream and
	// filters it the same way.
	if link != keepDelta {
		t.Errorf("deltaLink = %q, want Graph's own %q", link, keepDelta)
	}
	// The paging counted Graph's pages, not the kept objects.
	if want := []string{"/users/delta?$select=id%2CdisplayName%2Cmail%2Cdepartment", keepNext}; strings.Join(c.paths, " ") != strings.Join(want, " ") {
		t.Errorf("requested %v, want %v", c.paths, want)
	}
	res := got["konten"].(map[string]any)
	if res["withheld"] != 3 || res["undecided"] != 1 {
		t.Errorf("withheld/undecided = %v/%v, want 3/1 (r1, r2 do not match; r4 lacks department)", res["withheld"], res["undecided"])
	}
}

// The property this whole attribute exists for: nothing about a withheld object is in
// the result — not its id, not a name, not an address — and a removed object carries
// its id and @removed and nothing else.
func TestDeltaKeepResultHoldsNothingOfAWithheldObject(t *testing.T) {
	c := &pagingClient{pages: sharedTenantPages()}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten",
		Select: "id,displayName,mail,department", Keep: "department eq 'atlas-test-58o4'",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"r1", "r2", "r4", "Real Person", "contoso.example", "Finance"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the result carries %q of a withheld object: %s", leak, raw)
		}
	}
	list := got["konten"].(map[string]any)["value"].([]any)
	gone := list[1].(map[string]any)
	if len(gone) != 2 || gone["id"] != "gone" || gone["@removed"] == nil {
		t.Errorf("removed object = %#v, want only id and @removed", gone)
	}
}

// A keep over groups works the same way, and a kept group keeps its members@delta —
// ids, which the receiver translates or reports as unresolved.
func TestDeltaGroupsKeepByPrefix(t *testing.T) {
	members := []any{map[string]any{"id": "t1", "@odata.type": "#microsoft.graph.user"}}
	c := &pagingClient{pages: []any{map[string]any{"value": []any{
		map[string]any{"id": "g1", "displayName": "atlas-test-58o4-spielwiese", "members@delta": members},
		map[string]any{"id": "g2", "displayName": "Finance Leadership", "members@delta": members},
	}, "@odata.deltaLink": keepDelta}}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-groups", ResultVariable: "gruppen",
		Select: "id,displayName", Keep: "startswith(displayName,'atlas-test-58o4-')",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids, _ := deltaResult(t, got["gruppen"])
	if strings.Join(ids, ",") != "g1" {
		t.Errorf("kept = %v, want g1", ids)
	}
	g1 := got["gruppen"].(map[string]any)["value"].([]any)[0].(map[string]any)
	if _, ok := g1["members@delta"]; !ok {
		t.Errorf("kept group lost its members@delta: %#v", g1)
	}
}

// maxUsers counts what is kept: a keep over a large tenant does not fail on objects it
// was never going to pass on — and a keep that keeps too many still fails, rather than
// truncating.
func TestDeltaKeepMaxUsersCountsWhatIsKept(t *testing.T) {
	job := Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten",
		Select: "id,displayName,mail,department", Keep: "department eq 'atlas-test-58o4'", MaxUsers: 3,
	}
	// Six objects from Graph, three kept: within the cap.
	if _, err := Run(context.Background(), job, regWith("contoso", &pagingClient{pages: sharedTenantPages()})); err != nil {
		t.Fatalf("Run under the cap: %v", err)
	}
	job.MaxUsers = 2
	_, err := Run(context.Background(), job, regWith("contoso", &pagingClient{pages: sharedTenantPages()}))
	if err == nil || !strings.Contains(err.Error(), "maxUsers cap") {
		t.Errorf("err = %v, want three kept objects to fail a cap of two", err)
	}
}

// A run whose every object is withheld still ends with Graph's cursor and an empty
// change set: smaller than one page is the normal case in a shared tenant.
func TestDeltaKeepMayAnswerNothing(t *testing.T) {
	c := &pagingClient{pages: []any{map[string]any{"value": []any{
		map[string]any{"id": "r1", "department": "Finance"},
		"not an object",
	}, "@odata.deltaLink": keepDelta}}}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten",
		Select: "id,department", Keep: "department eq 'atlas-test-58o4'",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := got["konten"].(map[string]any)
	if list := res["value"].([]any); len(list) != 0 {
		t.Errorf("value = %#v, want empty", list)
	}
	if res["deltaLink"] != keepDelta || res["withheld"] != 2 {
		t.Errorf("result = %#v, want the cursor and two withheld", res)
	}
}

// Without a keep a delta answers as it always did: no counts, every object as sent.
func TestDeltaWithoutKeepIsUnchanged(t *testing.T) {
	c := &pagingClient{pages: sharedTenantPages()}
	got, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten",
	}, regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := got["konten"].(map[string]any)
	if _, ok := res["withheld"]; ok {
		t.Errorf("an unfiltered delta reports withheld: %#v", res)
	}
	if n := len(res["value"].([]any)); n != 6 {
		t.Errorf("value has %d objects, want all 6", n)
	}
}

// A keep the compiler would have refused, arriving in a job built by hand or by an
// older engine, fails the job; it never answers unfiltered. And keep belongs to the
// delta operations alone.
func TestDeltaKeepRefusals(t *testing.T) {
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "konten", Keep: "department ne 'x'",
	}, regWith("contoso", &pagingClient{pages: sharedTenantPages()}))
	if err == nil || !strings.Contains(err.Error(), `"ne"`) {
		t.Errorf("err = %v, want the bad keep refused", err)
	}
	_, err = Run(context.Background(), Job{
		Connector: "contoso", Operation: "list-users", ResultVariable: "r", Keep: "department eq 'x'",
	}, regWith("contoso", &pagingClient{pages: []any{page("", "u1")}}))
	if err == nil || !strings.Contains(err.Error(), "takes no keep") {
		t.Errorf("err = %v, want keep refused on a listing", err)
	}
}

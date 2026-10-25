package carddav

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/carddav/catalog"
)

// TestAgainstARealServer walks every operation against a real CardDAV address book, and
// is skipped unless one is named:
//
//	ATLAS_TEST_CARDDAV_URL       the Worker's endpoint
//	ATLAS_TEST_CARDDAV_BUNDLE    its credential bundle, {"username", "password", "groups"}
//	ATLAS_TEST_CARDDAV_GROUP     for the group-vcards form: a group that exists (Apple
//	                             Contacts cannot be asked to create one from here)
//
// It is the check ADR-0490's open question asks for: it creates one
// contact with a name no person has, changes it, puts it in a group and takes it out
// again, and deletes it. What the provider's own app shows meanwhile is the part a test
// cannot see — look at it.
func TestAgainstARealServer(t *testing.T) {
	endpoint := os.Getenv("ATLAS_TEST_CARDDAV_URL")
	if endpoint == "" {
		t.Skip("set ATLAS_TEST_CARDDAV_URL and ATLAS_TEST_CARDDAV_BUNDLE to run against a real address book")
	}
	c, err := NewClient(endpoint, os.Getenv("ATLAS_TEST_CARDDAV_BUNDLE"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("live", c)
	ctx := context.Background()
	do := func(j Job) any {
		t.Helper()
		j.Connector = "live"
		if op, _ := catalog.Lookup(j.Operation); op.TakesResult() {
			j.ResultVariable = "r"
		}
		out, err := Run(ctx, j, reg)
		if err != nil {
			t.Fatalf("%s: %v", j.Operation, err)
		}
		return out["r"]
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	email := "atlas-" + stamp + "@example.invalid"
	group := os.Getenv("ATLAS_TEST_CARDDAV_GROUP")
	if c.groups == catalog.GroupsCategories {
		group = "Atlas-Test"
	}
	contact := map[string]any{
		"givenName": "Atlas", "familyName": "Test " + stamp, "email": email,
		"phones":   []any{map[string]any{"value": "+41 79 000 00 00", "type": "mobile"}},
		"address":  map[string]any{"streetAddress": "Teststrasse 1", "city": "Bern", "postalCode": "3000", "country": "Schweiz", "type": "work"},
		"birthday": "--05-17", "notes": "Angelegt von TestAgainstARealServer", "fields": map[string]any{"Kundennummer": stamp},
	}
	if group != "" {
		contact["groups"] = []any{group}
	}
	key := CreateKey("live-test", uint64(time.Now().UnixNano()))
	created := do(Job{Operation: "create-contact", CreateKey: key, Contact: contact}).(map[string]any)
	id, _ := created["id"].(string)
	t.Logf("created %s", id)
	defer func() {
		if _, err := Run(ctx, Job{Connector: "live", Operation: "delete-contact", ContactID: id}, reg); err != nil {
			t.Errorf("clean up: %v", err)
		}
	}()
	// The same job again: refused with 412, answered with the contact the first round made.
	again := do(Job{Operation: "create-contact", CreateKey: key, Contact: contact}).(map[string]any)
	if again["id"] != id {
		t.Errorf("a repeated create answered %v, want %s", again["id"], id)
	}
	got := do(Job{Operation: "get-contact", ContactID: id}).(map[string]any)
	for k, want := range map[string]any{"email": email, "birthday": "--05-17", "displayName": "Atlas Test " + stamp} {
		if got[k] != want {
			t.Errorf("read back %s = %v, want %v", k, got[k], want)
		}
	}
	inGroup := func(groups any) bool {
		return strings.Contains(strings.ToLower(jsonOf(groups)), strings.ToLower(`"`+group+`"`))
	}
	if group != "" && !inGroup(got["groups"]) {
		t.Errorf("groups = %v, want %s", got["groups"], group)
	}
	if hits := do(Job{Operation: "find-contacts", Query: strings.ToUpper(email), MaxResults: 10}).([]any); len(hits) != 1 {
		t.Errorf("find by e-mail found %d", len(hits))
	}
	if hits := do(Job{Operation: "find-contacts", Query: stamp, Field: "Kundennummer", MaxResults: 10}).([]any); len(hits) != 1 {
		t.Errorf("find by field found %d", len(hits))
	}
	if hits := do(Job{Operation: "search-contacts", Query: "test " + stamp, MaxResults: 10}).([]any); len(hits) != 1 {
		t.Errorf("search found %d", len(hits))
	}
	updated := do(Job{Operation: "update-contact", ContactID: id, Contact: map[string]any{
		"email": "neu-" + email, "fields": map[string]any{"Segment": "A"}, "notes": "",
	}}).(map[string]any)
	if updated["email"] != "neu-"+email || updated["notes"] != nil || !strings.Contains(jsonOf(updated["fields"]), `"Segment":"A"`) {
		t.Errorf("updated = %v", updated)
	}
	if group != "" {
		do(Job{Operation: "remove-from-group", ContactID: id, Group: group})
		if g := do(Job{Operation: "get-contact", ContactID: id}).(map[string]any)["groups"]; inGroup(g) {
			t.Errorf("still in %s: %v", group, g)
		}
		do(Job{Operation: "add-to-group", ContactID: id, Group: group})
		if l := do(Job{Operation: "list-contacts", Group: group, MaxResults: 10000}).([]any); !strings.Contains(ids(l), id) {
			t.Errorf("the group's listing lacks the contact")
		}
	}
	do(Job{Operation: "delete-contact", ContactID: id})
	_, err = Run(ctx, Job{Connector: "live", Operation: "get-contact", ContactID: id, ResultVariable: "r"}, reg)
	var nf *notFound
	if !errors.As(err, &nf) {
		t.Errorf("a deleted contact read as %v", err)
	}
}

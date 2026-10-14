package api

import (
	"strings"
	"testing"
)

// Who, picked rather than typed (the catalogue's authoring screen).
//
// The module's own header states the rule these guards extend: it binds processes
// from what is deployed and never from free text, because a product naming a
// process nobody wrote is an order that fails while somebody waits for a laptop.
// Every field about *people* was the exception — each asked for an opaque id,
// typed from memory.
//
// It was not merely inconvenient. The audience field's placeholder read
// "kunde-a, kunde-b" — names — while Catalog.ReachedBy compares its entries
// against the group ids a session carries. Following the placeholder produced a
// catalogue reaching nobody, with no error anywhere; and the hint beside the
// sharing form sent the reader to Console → Organization, which shows group names
// and not their ids.
//
// The guards read the region they guard. "principals" and "dir" appear in half the
// module, so a search across the file would pass whatever the controls did.

// TestTheAudienceIsPickedAndNotTyped.
//
// The control that decides who reaches a catalogue is the one place where a typo
// costs the most and shows the least: an unreachable catalogue looks exactly like
// one nobody has filled in yet.
func TestTheAudienceIsPickedAndNotTyped(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	body := webRegion(t, src, "function audienceField(dir, chosen)", "\n}")

	if !strings.Contains(body, "groupChoices(dir)") {
		t.Error("the audience control does not read the directory, so it is still asking " +
			"somebody to remember a grp_ id")
	}
	if !strings.Contains(body, `type="checkbox" name="groups"`) {
		t.Error("the audience is not offered as choices; a free-text field is what this replaced")
	}
	// The placeholder that taught the mistake: it suggested names where the server
	// compares ids, so following it produced an audience reaching nobody. Asserted
	// as the attribute and not as the bare word, because the comment above the
	// control quotes it to explain why it is gone — a guard that forbade the word
	// would be satisfied by deleting the explanation.
	if strings.Contains(src, `placeholder="kunde-a`) {
		t.Error("the placeholder suggesting group names is still on a field")
	}
}

// TestTheAudienceOffersGroupsAndNeverPeople.
//
// ReachedBy compares a catalogue's audience against group ids only. A picker that
// also offered accounts would manufacture the mistake it exists to prevent — and
// it would look right, because the sharing picker beside it legitimately offers
// both.
func TestTheAudienceOffersGroupsAndNeverPeople(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	body := webRegion(t, src, "function groupChoices(dir)", "\n}")
	if !strings.Contains(body, `p.type === "group"`) {
		t.Error("the audience choices are not filtered to groups, so a user id can be " +
			"named as an audience — where it matches nothing, in silence")
	}
}

// TestAGrantIsOneChoiceAndNotATypeBesideAnId.
//
// The kind and the id are one fact about one person. Asked separately, they can
// disagree — "One account" in front of a group id — and the pair that results names
// nobody.
func TestAGrantIsOneChoiceAndNotATypeBesideAnId(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	body := webRegion(t, src, "function shareWhoField(dir, cat)", "\n}")
	if !strings.Contains(body, `<select name="who"`) {
		t.Error("the sharing control does not offer one choice carrying both halves")
	}
	if !strings.Contains(body, `${esc(p.type)}|${esc(p.id)}`) {
		t.Error("the option does not carry the type beside the id, so the grant is built " +
			"from whatever the separate control happened to say")
	}

	// And the other end: the submit has to split what the option carried.
	handler := webRegion(t, src, `const shareNew = view.querySelector(".share-new");`, "\n  const edgeNew")
	if !strings.Contains(handler, `f.get("who")`) {
		t.Error("the submit still reads the two old fields, so the picker's answer is dropped")
	}
}

// TestAPickerWithNoDirectoryComesBackAsAnIdField.
//
// The degraded path is not a nicety. A picker with no options and no explanation is
// worse than the input it replaced: it looks like an answer — "there are no groups"
// — to a question it never asked. Both controls have to fall back, and the two
// cases have to stay distinguishable: a directory that could not be read is not an
// empty one.
func TestAPickerWithNoDirectoryComesBackAsAnIdField(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	for _, c := range []struct{ name, from string }{
		{"audience", "function audienceField(dir, chosen)"},
		{"sharing", "function shareWhoField(dir, cat)"},
	} {
		body := webRegion(t, src, c.from, "\n}")
		if !strings.Contains(body, "dir === null") {
			t.Errorf("the %s control does not distinguish a directory it could not read "+
				"from one that is empty", c.name)
		}
		if !strings.Contains(body, "could not be read") {
			t.Errorf("the %s control degrades without saying why, so an id field appears "+
				"where a picker was a moment ago", c.name)
		}
	}
}

// TestAnAudienceTheDirectoryNoLongerKnowsKeepsItsPlace.
//
// A checkbox list has a property a text field does not: not drawing a value and
// unticking it produce the same saved result. So a group deleted after it was named
// here would be dropped by the next save of an unrelated field, silently, and the
// catalogue would quietly stop reaching people it was still meant to reach.
func TestAnAudienceTheDirectoryNoLongerKnowsKeepsItsPlace(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function audienceField(dir, chosen)", "\n}")
	if !strings.Contains(body, "orphans") {
		t.Fatal("the audience control draws only what the directory carries, so saving the " +
			"form drops a group the directory has lost")
	}
	if !strings.Contains(body, "...choices, ...orphans") {
		t.Error("the groups the directory has lost are not drawn beside the ones it carries")
	}
}

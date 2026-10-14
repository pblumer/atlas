package api

import (
	"testing"

	"github.com/pblumer/atlas/api/order"
)

// The three sentences a reminder is built from (ADR-0343).
//
// Pure functions, and the whole point of a reminder rests on them: one wrong
// message costs more than ten right ones earn, because it teaches the reader
// that these are noise and the next one is deleted unread.

// TestAnApprovalHasWaitedSinceItReachedThisPerson.
//
// Not since the order was placed. An approval that escalated to somebody
// yesterday has waited a day, not a fortnight, and telling them otherwise is the
// small wrongness that makes the whole message suspect.
func TestAnApprovalHasWaitedSinceItReachedThisPerson(t *testing.T) {
	const sec = int64(1e9)

	if got := waitingSince(approvalResp{}); got != 0 {
		t.Errorf("an approval with no assignment record = %d, want 0 — nothing here "+
			"invents an age", got)
	}

	fresh := approvalResp{Assignment: &order.Assignment{AssignedAt: 100 * sec}}
	if got := waitingSince(fresh); got != 100 {
		t.Errorf("unescalated = %d, want the first assignment (100)", got)
	}

	moved := approvalResp{Assignment: &order.Assignment{
		AssignedAt: 100 * sec,
		Escalations: []order.Escalation{
			{At: 200 * sec}, {At: 300 * sec},
		},
	}}
	if got := waitingSince(moved); got != 300 {
		t.Errorf("escalated twice = %d, want the last hop (300) — the person holding it "+
			"now has not been holding it since the order was placed", got)
	}
}

// TestAProductIsNamedInWordsAndAlwaysTheSameWay.
//
// An approver deciding "vpn-zugang" is reading an id. The language is whichever
// sorts first, deterministically: the mail is addressed to one person whose
// language nothing here knows, and picking the first by name at least produces
// the same sentence twice.
func TestAProductIsNamedInWordsAndAlwaysTheSameWay(t *testing.T) {
	if got := productWords(approvalResp{ItemID: "vpn-zugang"}); got != "vpn-zugang" {
		t.Errorf("a product with no texts = %q, want its id — an invented name would be "+
			"worse than an ugly one", got)
	}

	a := approvalResp{ItemID: "vpn", Texts: map[string]string{
		"fr": "Accès VPN", "de": "VPN-Zugang", "en": "VPN access",
	}}
	first := productWords(a)
	if first != "VPN-Zugang" {
		t.Errorf("= %q, want the first language by name", first)
	}
	for i := 0; i < 20; i++ {
		if productWords(a) != first {
			t.Fatal("the same product is named differently on a second pass; a map's " +
				"iteration order reached the sentence")
		}
	}

	// A language present but blank falls back to the id rather than to an empty
	// sentence: "An order of  for usr_ada is waiting" reads as a defect.
	blank := approvalResp{ItemID: "vpn", Texts: map[string]string{"de": "   "}}
	if got := productWords(blank); got != "vpn" {
		t.Errorf("a blank text = %q, want the id", got)
	}
}

// TestTheApprovalSentenceSaysWhoItIsFor.
//
// And falls back through the orderer to nobody, because a sentence naming an
// empty person is what a reader reports as broken.
func TestTheApprovalSentenceSaysWhoItIsFor(t *testing.T) {
	both := approvalSentence(approvalResp{
		ItemID: "vpn", Recipient: "usr_ada", Orderer: "usr_chef",
	})
	if !containsSub(both, "usr_ada") || containsSub(both, "usr_chef") {
		t.Errorf("= %q, want the recipient named and not the orderer: the question is "+
			"who gets it", both)
	}

	ordererOnly := approvalSentence(approvalResp{ItemID: "vpn", Orderer: "usr_chef"})
	if !containsSub(ordererOnly, "usr_chef") {
		t.Errorf("= %q, want the orderer when there is no recipient", ordererOnly)
	}

	nobody := approvalSentence(approvalResp{ItemID: "vpn"})
	if containsSub(nobody, " for  ") {
		t.Errorf("= %q, want no dangling empty person", nobody)
	}
	if !containsSub(nobody, "vpn") {
		t.Errorf("= %q, want the product named", nobody)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

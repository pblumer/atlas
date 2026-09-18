package api

import (
	"strings"
	"testing"
)

// Following an order into the process that is fulfilling it.
//
// "Where is my order" is answered twice over on this screen, and the two answers
// are for two different readers. Every reader gets the positions and what each one
// is doing, out of the order's own record. An operator gets, in addition, a way
// into the running instance — which is an operations surface: it shows the whole
// engine state of that instance, and every route that finds or opens one is
// operator-only.
//
// So the link is offered to whoever may follow it and to nobody else. A link that
// answered 403 would be this page teaching its reader that it is broken, and a
// link that opened an operations console for an ordinary orderer would be worse.

// TestTheProcessLinkIsOfferedToWhoMayFollowIt.
func TestTheProcessLinkIsOfferedToWhoMayFollowIt(t *testing.T) {
	src := readWeb(t, "portal.js")
	rows := webRegion(t, src, "function orderRowBodies(", "\n}")
	if !strings.Contains(rows, "state.mayFollowProcess") {
		t.Error("the row offers the process link to everybody, or to nobody, without " +
			"asking whether this reader may follow it")
	}
	// And the flag is the roles the instance routes actually require, read from the
	// same record every other role on this page comes from.
	who := webRegion(t, src, "function loadWhoIAm(", "\n}")
	if !strings.Contains(who, "state.mayFollowProcess") {
		t.Error("nothing decides whether this reader may follow a process, so the flag " +
			"is whatever it was left as")
	}
}

// TestTheInstanceIsFoundByTheOrderItIsFor.
//
// And by the fulfilment process, not by whatever else carries the same variable:
// every provisioning sub-process is started with the order id too, so a search
// that took the first hit would open one position's process and call it the order.
func TestTheInstanceIsFoundByTheOrderItIsFor(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function followProcess(", "\n}")
	if !strings.Contains(body, "/api/v1/instances/search") {
		t.Error("nothing looks the instance up, so the link cannot know where to go")
	}
	if !strings.Contains(body, "orderId=") {
		t.Error("the lookup does not name the order it is for")
	}
	if !strings.Contains(body, "atlas-auftrag-erfuellung") {
		t.Error("the lookup does not say which process it wants, so it may open a " +
			"provisioning sub-process and call it the order")
	}
	if !strings.Contains(body, "#/operations/i/") {
		t.Error("the link does not lead to the instance view")
	}
}

// TestAnInstanceThatIsGoneIsSaidRatherThanFollowed.
//
// History retention deletes an instance long before the order is deleted, so an
// order whose process is gone is the ordinary late case rather than an error. A
// link that navigated to nothing would look like the console had broken.
func TestAnInstanceThatIsGoneIsSaidRatherThanFollowed(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "async function followProcess(", "\n}")
	if !strings.Contains(body, "proc.none") {
		t.Error("an order whose instance no longer exists follows a link to nowhere " +
			"instead of being told that the process has been cleaned up")
	}
}

// TestAMissingInstanceIsNotBlamedOnRetention.
//
// The message said the instance "was removed by retention". That is one of three
// reasons it is not found, and the least likely of them: an order whose fulfilment
// never started has no instance to remove, and that is what somebody reads this
// about on the day they ordered — which is exactly the case a real defect in the
// fulfilment wake produced for every order on an installation.
//
// A page that names a cause it cannot know sends whoever reads it to look in the
// wrong place, and "retention removed it" reads as "it is gone for good".
func TestAMissingInstanceIsNotBlamedOnRetention(t *testing.T) {
	src := readWeb(t, "portal.js")
	for _, key := range []string{"'proc.none'", "'proc.none.order'"} {
		for _, locale := range []struct{ name, from, to string }{
			{"de", "  de: {", "\n  },"},
			{"en", "  en: {", "\n  },"},
		} {
			body := webRegion(t, src, locale.from, locale.to)
			at := strings.Index(body, key)
			if at < 0 {
				t.Errorf("%s carries no %s, so one locale says nothing where the other "+
					"explains", locale.name, key)
				continue
			}
			line := body[at : at+strings.Index(body[at:], "\n")]
			// Three causes, and the one that was missing is the one that matters: a
			// process nobody has started yet.
			if !strings.Contains(line, "gestartet") && !strings.Contains(line, "started") {
				t.Errorf("%s %s names no cause but the ones it can see afterwards; the "+
					"ordinary case — nothing has started yet — is not among them: %s",
					locale.name, key, line)
			}
		}
	}
	// And the two are told apart, because the order's own orchestration and one
	// position's process are two different absences.
	body := webRegion(t, src, "async function followProcess(", "\n}")
	if !strings.Contains(body, "'proc.none.order'") {
		t.Error("the order's missing instance is reported in the words written for a " +
			"position, which names the wrong thing to whoever reads it")
	}
}

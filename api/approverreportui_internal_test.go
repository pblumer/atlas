package api

import (
	"strings"
	"testing"
)

// The standing list, where the person who writes approval rules will see it.
//
// A report that exists only as an endpoint is a report somebody has to think to
// run, and nobody thinks to run one for a defect they do not know they have. It
// sits on the catalogue listing, which is the first screen of the role that writes
// these rules.

// TestTheCatalogueListingCarriesTheApproverReport.
func TestTheCatalogueListingCarriesTheApproverReport(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	listing := webRegion(t, src, "export async function viewCatalogs(", "\n}")

	if !strings.Contains(listing, "/api/v1/catalog-products/approver-report") {
		t.Error("the catalogue listing does not ask for the approver report, so a rule that " +
			"reaches nobody is visible only to somebody who opens that one product")
	}
	if !strings.Contains(listing, "approverCard(report)") {
		t.Error("the report is fetched and not drawn")
	}
}

// TestTheApproverCardSaysWhichOfItsThreeAnswersItIsGiving.
//
// The third state is why this is a card and not a line. "No problems" and "I could
// not read the report" look identical if the second is drawn as the first — and the
// second is the one somebody wants to believe.
func TestTheApproverCardSaysWhichOfItsThreeAnswersItIsGiving(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function approverCard(report)", "\n}")

	if !strings.Contains(body, "report === null") {
		t.Fatal("the card does not distinguish a report it could not read from one with no " +
			"problems, so a server that cannot resolve accounts renders as a clean estate")
	}
	if !strings.Contains(body, "could not be read") {
		t.Error("the unreadable case says nothing about itself")
	}
	// An empty list without a count reads as "nothing was checked". The count is
	// what makes it an answer.
	if !strings.Contains(body, "${checked} product") {
		t.Error("the all-clear does not say how many products it looked at")
	}
	if !strings.Contains(body, "problems.length} of ${checked}") {
		t.Error("the problem list does not say how many of how many")
	}
	// Every row points at the catalogue where the product is maintained, because
	// that is where the reader has to go to correct it.
	if !strings.Contains(body, "#/catalog/c/${encodeURIComponent(p.homeCatalog)}") {
		t.Error("a row does not link the home catalogue, so the reader is told about a " +
			"defect and not where to fix it")
	}
}

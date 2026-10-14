package catalog

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// fakeProcesses is an engine that says what a test tells it to.
type fakeProcesses struct {
	// deployed maps a process id to the job types its tasks carry. A process id
	// absent from the map is not deployed.
	deployed map[string][]string
	// unserved is the set of job types nothing works.
	unserved map[string]bool
}

func (f fakeProcesses) JobTypesOf(id string) ([]string, bool) {
	jt, ok := f.deployed[id]
	return jt, ok
}
func (f fakeProcesses) Served(jobType string) bool { return !f.unserved[jobType] }

// healthy is an installation where everything the catalogue names works.
func healthy() fakeProcesses {
	return fakeProcesses{deployed: map[string][]string{
		OrderProcess:                     {"rest"},
		"atlas-genehmigung-fix":          {"io.atlas.user-task", "rest"},
		"atlas-genehmigung-rolle":        {"io.atlas.user-task", "rest"},
		"atlas-genehmigung-vorgesetzter": {"io.atlas.user-task", "rest", "ad"},
		"proc_laptop_apple_ausgabe":      {"io.atlas.user-task"},
		"proc_laptop_apple_ruecknahme":   {"io.atlas.user-task"},
	}, unserved: map[string]bool{}}
}

// aMacBook is the product the report was written from.
func aMacBook() Item {
	return Item{
		ID: "laptop-apple-hw", HomeCatalog: "cat_demo",
		Approval:           Approval{Kind: KindNone},
		ProvisionProcess:   "proc_laptop_apple_ausgabe",
		DeprovisionProcess: "proc_laptop_apple_ruecknahme",
	}
}

// TestAnInstallationThatWorksReportsNothing.
//
// The report has to be quiet where there is nothing to say, or it is a list
// nobody reads and the one real finding sits in the middle of it.
func TestAnInstallationThatWorksReportsNothing(t *testing.T) {
	got := fulfilmentProblems([]Item{aMacBook()}, healthy())
	if len(got) != 0 {
		t.Errorf("a working installation reports %d problem(s), want none: %+v", len(got), got)
	}
}

// TestTheJobTypeNobodyWorksIsNamed.
//
// The whole point. A token parked on an unserved job type does not fail: no
// retry is spent, no incident is raised, and the queue looks exactly like one
// that is draining. This is the only place it is said.
func TestTheJobTypeNobodyWorksIsNamed(t *testing.T) {
	eng := healthy()
	eng.unserved = map[string]bool{"rest": true}

	got := fulfilmentProblems([]Item{aMacBook()}, eng)
	if len(got) != 1 {
		t.Fatalf("got %d problems, want the one on the shared path: %+v", len(got), got)
	}
	p := got[0]
	if p.Stage != "order" || p.ProcessID != OrderProcess || p.JobType != "rest" {
		t.Errorf("the orchestration's unserved job type is reported as %+v", p)
	}
	if p.ItemID != "" {
		t.Errorf("a problem on the shared path is filed against the product %q; it "+
			"belongs to no product and stops every order alike", p.ItemID)
	}
	// And it says what a reader can act on, rather than restating the binding.
	for _, want := range []string{"nothing here works", "no", "incident"} {
		if !strings.Contains(p.Why, want) {
			t.Errorf("the reason %q does not say %q — the silence is the finding, so "+
				"the reason has to name it", p.Why, want)
		}
	}
}

// TestTheSharedPathIsReportedOnceAndNotPerProduct.
//
// Ten products behind one broken orchestration is one fact, not ten. Reported per
// product it would bury every product-specific finding under a repetition.
func TestTheSharedPathIsReportedOnceAndNotPerProduct(t *testing.T) {
	eng := healthy()
	eng.unserved = map[string]bool{"rest": true}

	var items []Item
	for _, id := range []string{"a", "b", "c", "d"} {
		it := aMacBook()
		it.ID = id
		items = append(items, it)
	}
	got := fulfilmentProblems(items, eng)
	if len(got) != 1 {
		t.Errorf("four products behind one broken orchestration report %d problems, "+
			"want 1: %+v", len(got), got)
	}
}

// TestAnApprovalProcessIsWalkedToo.
//
// The stage between the order and the work, and the one a product-only check
// misses entirely: the rule resolves, the approver exists, the provisioning
// process is fine — and the approval never starts.
func TestAnApprovalProcessIsWalkedToo(t *testing.T) {
	eng := healthy()
	delete(eng.deployed, "atlas-genehmigung-fix")

	it := aMacBook()
	it.Approval = Approval{Kind: KindFixed, Ref: "Sven"}
	got := fulfilmentProblems([]Item{it}, eng)
	if len(got) != 1 {
		t.Fatalf("got %d problems, want the missing approval process: %+v", len(got), got)
	}
	if got[0].Stage != "approval" || got[0].ProcessID != "atlas-genehmigung-fix" {
		t.Errorf("the approval stage is reported as %+v", got[0])
	}
	if got[0].JobType != "" {
		t.Errorf("a process that is not deployed carries a job type %q in the report; "+
			"there is no model to read one from", got[0].JobType)
	}
}

// TestAProductThatNeedsNoApprovalWalksNoApprovalProcess.
//
// Every product in the release that reported this was approval-free. A check that
// walked an approval process anyway would report a stage no order of theirs takes.
func TestAProductThatNeedsNoApprovalWalksNoApprovalProcess(t *testing.T) {
	eng := healthy()
	delete(eng.deployed, "atlas-genehmigung-fix")
	if got := fulfilmentProblems([]Item{aMacBook()}, eng); len(got) != 0 {
		t.Errorf("a product with approval kind %q is checked against an approval "+
			"process: %+v", KindNone, got)
	}
}

// TestAProcessThatWasNeverDeployedIsNamedAsSuch.
//
// The other half of a binding's failure, and the one an operator fixes
// differently: a name to correct rather than a worker to start.
func TestAProcessThatWasNeverDeployedIsNamedAsSuch(t *testing.T) {
	eng := healthy()
	it := aMacBook()
	it.ProvisionProcess = "proc_tippfehler"

	got := fulfilmentProblems([]Item{it}, eng)
	if len(got) != 1 {
		t.Fatalf("got %d problems, want the undeployed process: %+v", len(got), got)
	}
	if got[0].Stage != "provision" || got[0].ItemID != "laptop-apple-hw" {
		t.Errorf("the product's own binding is reported as %+v", got[0])
	}
	if !strings.Contains(got[0].Why, "no deployed model") {
		t.Errorf("the reason %q does not say the model is absent", got[0].Why)
	}
	// Filed against the catalogue it is maintained in, because that is where the
	// reader goes to correct it.
	if got[0].HomeCatalog != "cat_demo" {
		t.Errorf("the problem names home catalogue %q", got[0].HomeCatalog)
	}
}

// TestGivingSomethingBackIsCheckedAsWell.
//
// The same silence, later: a service that cannot be returned parks on the day
// somebody leaves, which is the worst day to discover it.
func TestGivingSomethingBackIsCheckedAsWell(t *testing.T) {
	eng := healthy()
	delete(eng.deployed, "proc_laptop_apple_ruecknahme")

	got := fulfilmentProblems([]Item{aMacBook()}, eng)
	if len(got) != 1 || got[0].Stage != "deprovision" {
		t.Errorf("the return binding is not checked: %+v", got)
	}
}

// TestABindingToNothingIsNotAFinding.
//
// An item that is only ever a part of something else provisions through the whole
// and names no process of its own. That is a catalogue decision, and reporting it
// would put every such part on a list of defects.
func TestABindingToNothingIsNotAFinding(t *testing.T) {
	it := aMacBook()
	it.ProvisionProcess, it.DeprovisionProcess = "", "   "
	if got := fulfilmentProblems([]Item{it}, healthy()); len(got) != 0 {
		t.Errorf("an item bound to no process is reported: %+v", got)
	}
}

// TestTheLookupDecidesWhatCountsAsWorked.
//
// Who works a job type is the engine's question: it serves some itself, people
// serve user tasks, and workers serve the rest. The report must not hold a second
// opinion — a skip list here would go stale the day the engine gains a type, and
// it would go stale silently, which is the failure this whole report is about.
//
// So: a lookup that says a user-task type is unworked must be believed. Nothing
// in a real installation says that, and that is the point — the assertion is that
// the report has no rule of its own to say otherwise.
func TestTheLookupDecidesWhatCountsAsWorked(t *testing.T) {
	eng := healthy()
	eng.unserved = map[string]bool{"io.atlas.user-task": true}

	it := aMacBook()
	it.Approval = Approval{Kind: KindFixed, Ref: "Sven"}
	got := fulfilmentProblems([]Item{it}, eng)

	var stages []string
	for _, p := range got {
		if p.JobType != "io.atlas.user-task" {
			t.Errorf("an unrelated job type is reported: %+v", p)
			continue
		}
		stages = append(stages, p.Stage)
	}
	// The approval process, the provisioning and the return all carry one.
	for _, want := range []string{"approval", "provision", "deprovision"} {
		found := false
		for _, s := range stages {
			found = found || s == want
		}
		if !found {
			t.Errorf("the report second-guesses the lookup at stage %q: it decided the "+
				"job type is unworked and the report dropped it", want)
		}
	}
}

// TestTheFulfilmentReportFollowsTheCatalogueYouMaintain.
//
// The report names this installation's wiring — which processes exist and which
// job types nothing works — so it reaches exactly as far as the product listing
// and the approver report do, and no further. Cited by name from the gate
// inventory, which is where the claim that this handler needs no object gate is
// written down.
func TestTheFulfilmentReportFollowsTheCatalogueYouMaintain(t *testing.T) {
	s := serviceWithAdmin(t)
	s.Processes = healthy()
	mine := makeCatalog(t, s, user("usr_a"))
	theirs := makeCatalog(t, s, user("usr_b"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", boundBody("mine", mine.ID, "proc_tippfehler"))
	as(t, s.HandleSaveItem, user("usr_b"), "POST", boundBody("theirs", theirs.ID, "proc_tippfehler"))
	offerAndPublish(t, s, user("usr_a"), mine.ID, "mine", 11)
	offerAndPublish(t, s, user("usr_b"), theirs.ID, "theirs", 12)

	got := decode[FulfilmentReport](t, as(t, s.HandleFulfilmentReport, user("usr_a"), "GET", ""))
	if got.Checked != 1 {
		t.Errorf("checked = %d, want only the one service I offer", got.Checked)
	}
	for _, p := range got.Problems {
		if p.ItemID != "" && p.ItemID != "mine" {
			t.Errorf("the report names %q, which is somebody else's service — and with "+
				"it the processes their installation is wired to", p.ItemID)
		}
	}
}

// TestACatalogueNobodyHasPublishedIsNotReportedOn.
//
// Nothing can be ordered from it, so nothing it names can park. A finding against
// a catalogue somebody is still filling would be a list that is never empty and
// therefore never read.
func TestACatalogueNobodyHasPublishedIsNotReportedOn(t *testing.T) {
	s := serviceWithAdmin(t)
	s.Processes = healthy()
	cat := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", boundBody("draft", cat.ID, "proc_tippfehler"))
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"items":["draft"]}`, "id", cat.ID)

	got := decode[FulfilmentReport](t, as(t, s.HandleFulfilmentReport, user("usr_a"), "GET", ""))
	if got.Checked != 0 || len(got.Releases) != 0 {
		t.Errorf("an unpublished catalogue is reported on: checked=%d releases=%v",
			got.Checked, got.Releases)
	}
}

// TestWithoutAnEngineTheFulfilmentReportRefusesRatherThanGuesses.
//
// The approver report's reasoning exactly, and the dangerous guess is the same
// one: "everything is fine" is the answer somebody wants to see, and it would be
// given by a server that cannot see anything at all.
func TestWithoutAnEngineTheFulfilmentReportRefusesRatherThanGuesses(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", boundBody("laptop", cat.ID, "proc_tippfehler"))

	rec := as(t, s.HandleFulfilmentReport, user("usr_a"), "GET", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when nothing can say what is deployed; body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot say") {
		t.Errorf("the refusal does not say what it cannot do: %s", rec.Body.String())
	}
}

// offerAndPublish puts the service in the catalogue and publishes it, because the
// report reads the release rather than the live record.
func offerAndPublish(t *testing.T, s *Service, p *httpapi.Principal, catID, itemID string, rank int) {
	t.Helper()
	body := `{"rank":` + strconv.Itoa(rank) + `,"items":["` + itemID + `"]}`
	if rec := as(t, s.HandleUpdateCatalog, p, "PATCH", body, "id", catID); rec.Code != http.StatusOK {
		t.Fatalf("offer %s: %d (%s)", itemID, rec.Code, rec.Body.String())
	}
	if rec := as(t, s.HandlePublish, p, "POST", "", "id", catID); rec.Code != http.StatusCreated {
		t.Fatalf("publish %s: %d (%s)", catID, rec.Code, rec.Body.String())
	}
}

// boundBody is a product bound to the process named.
func boundBody(id, home, provision string) string {
	return `{"id":"` + id + `","homeCatalog":"` + home + `","state":"active",` +
		`"texts":{"de":"X"},"approval":{"kind":"none"},` +
		`"provisionProcess":"` + provision + `","deprovisionProcess":"` + provision + `"}`
}

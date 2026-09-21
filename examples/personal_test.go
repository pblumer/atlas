package examples

import (
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The proof ADR-0314 asks for and could not give itself.
//
// Its open question was whether every portal process can be written without computing on a
// declared personal variable, and it said plainly that this was "established by no portal
// process that exists yet". account-bestellung is that process: it took Vorname and
// Nachname from a start form and built a UPN, a mailNickname and a display name out of
// them — in one zeebe:script and three output mappings, all evaluated by the engine.
//
// It is established now, and this test is where it stays established. Restructuring the
// example is only evidence for as long as nobody puts the transform back, and the record's
// claim would otherwise rest on a commit message.
//
// What the restructuring cost is recorded rather than glossed: the example had a
// fail-closed gateway checking `starts with(upn, "jml-test-")` before any write, and with
// the construction moved into the worker there is no such process variable to check. The
// boundary is now carried by the literals in the connector's own attributes expression,
// which is in the BPMN and reviewable — structural rather than checked at runtime. That is
// a real trade and ADR-0314 §"the consequence that bites" now names it.
func TestAccountBestellungHoldsUnderThePersonalDataRule(t *testing.T) {
	raw, err := os.ReadFile("account-bestellung/account-bestellung.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	model := string(raw)

	deployables, err := compiler.ParseAll(1, 1, strings.NewReader(model))
	if err != nil {
		t.Fatalf("the example no longer deploys: %v", err)
	}
	cp := deployables[0].Process

	// The declaration has to be live. A process that deploys because it declares nothing
	// would pass this test while proving nothing at all — which is the trap worth naming,
	// because it is the same shape as the rule's own failure mode.
	if !cp.IsPersonal("vorname") || !cp.IsPersonal("nachname") {
		t.Fatalf("PersonalVariables() = %v: the example does not declare the names it takes from its form, so this proves nothing",
			cp.PersonalVariables())
	}

	// And the rule has to bite on *this* model, not only on a fixture. Putting the transform
	// back where it used to be must be refused.
	broken := strings.Replace(model,
		`<zeebe:script resultVariable="kategorie" expression="= profil.kategorie"/>`,
		`<zeebe:script resultVariable="kategorie" expression="= profil.kategorie"/>`+"\n"+
			`        <zeebe:ioMapping><zeebe:output source="= lower case(vorname)" target="mailNick"/></zeebe:ioMapping>`, 1)
	if broken == model {
		t.Fatal("could not build the violating variant: the anchor in the model moved, so this test is no longer checking what it claims")
	}
	if _, err := compiler.ParseAll(1, 1, strings.NewReader(broken)); err == nil {
		t.Error("putting the personal transform back into an output mapping was accepted; the rule does not bite on this model")
	}
}

// TestAccountBestellungNamesWhoseDataItHolds is the second thing the real process taught,
// and it is not a detail of the example. Crypto-shredding encrypts under a key belonging
// to *a person*, so a model that declares personal data must also say which person —
// with an identifier a deletion request can name. A first name and a last name are not
// such an identifier.
//
// For an account order that identifier necessarily comes from outside Atlas: the subject
// is a joiner who has no principal in the tenant yet, because the account is what is
// being ordered. So the start form grew a Personalnummer that no business requirement
// asked for. That is a cost of the mechanism, paid in the model, and it is worth pinning
// here — the alternative designs (a key per instance, the starter as subject) are exactly
// the ones this assertion would let through.
func TestAccountBestellungNamesWhoseDataItHolds(t *testing.T) {
	raw, err := os.ReadFile("account-bestellung/account-bestellung.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	deployables, err := compiler.ParseAll(1, 1, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("the example no longer deploys: %v", err)
	}
	cp := deployables[0].Process

	subject := cp.DataSubjectVariable()
	if subject == "" {
		t.Fatal("the example declares personal data but names no data subject, so nothing could ever be erased")
	}
	if cp.IsPersonal(subject) {
		t.Errorf("the data subject %q is itself declared personal, which would hide the name of its own key", subject)
	}
	// The form has to ask for it. A declaration naming a variable no form fills and no
	// worker writes would leave every instance with an empty subject — and the edge
	// refuses to encipher then, so every order would fail at the first personal value.
	form, err := os.ReadFile("account-bestellung/form-account-order.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(form), `"key": "`+subject+`"`) {
		t.Errorf("the start form does not collect %q, so no instance would have a data subject", subject)
	}
	// And it stays findable: erasing a person starts with finding their instances.
	if !cp.IsSearchableVariable(subject) {
		t.Errorf("the data subject %q is not searchable, so an operator cannot find the instances to erase", subject)
	}
}

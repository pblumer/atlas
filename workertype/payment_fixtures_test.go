package workertype

import (
	"encoding/json"
	"os"
	"testing"
)

// The payment fixtures illustrate the contracts ADR-0529
// proposes for a Datatrans Worker Type. Like every contract under testdata they
// are examples of the envelope, not a published corpus or provider evidence
// (ADR-0485). What these tests pin is the part of the proposal that must not
// erode while it is still a draft: card data has no place in any shape, an
// operation that moves money cannot be called without an idempotency key, and
// amounts are whole minor units in an ISO 4217 currency.

type paymentFixture struct {
	name         string
	resourceKind string
	verb         string
	movesMoney   bool
}

var paymentFixtures = []paymentFixture{
	{"payment-checkout-create", "payment-checkout", "create", true},
	{"payment-transaction-read", "payment-transaction", "read", false},
	{"payment-transaction-capture", "payment-transaction", "capture", true},
	{"payment-transaction-cancel", "payment-transaction", "cancel", false},
	{"payment-refund-create", "payment-refund", "create", true},
}

func loadPaymentCases(t *testing.T, name string) []TechnicalSchemaFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/technical-" + name + "-schema-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []TechnicalSchemaFixture
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// firstValid returns a copy of the first case marked valid for direction, as
// a mutable object, so a test can add or remove one property from a document
// the contract is known to accept.
func firstValid(t *testing.T, cases []TechnicalSchemaFixture, direction string) map[string]any {
	t.Helper()
	for _, c := range cases {
		if c.Direction == direction && c.Valid {
			var doc map[string]any
			if err := json.Unmarshal(c.Document, &doc); err != nil {
				t.Fatal(err)
			}
			return doc
		}
	}
	t.Fatalf("no valid %s case", direction)
	return nil
}

func validate(t *testing.T, compiled *CompiledTechnicalContract, direction string, doc map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if direction == "input" {
		return compiled.ValidateInputJSON(raw)
	}
	return compiled.ValidateOutputJSON(raw)
}

func TestPaymentContractFixturesFormADistinctSet(t *testing.T) {
	var contracts []TechnicalContract
	for _, f := range paymentFixtures {
		c := fixtureContract(t, "technical-"+f.name+".json")
		if c.Metadata.Profile != "datatrans" {
			t.Errorf("%s: profile %q, want the provider-specific profile datatrans", f.name, c.Metadata.Profile)
		}
		if c.Spec.ResourceKind != f.resourceKind || c.Spec.Verb != f.verb {
			t.Errorf("%s: %s/%s, want %s/%s", f.name, c.Spec.ResourceKind, c.Spec.Verb, f.resourceKind, f.verb)
		}
		if c.Spec.IdentityScope != "merchant-account" {
			t.Errorf("%s: identity scope %q, want merchant-account", f.name, c.Spec.IdentityScope)
		}
		contracts = append(contracts, c)
	}
	if err := ValidateTechnicalContracts(contracts); err != nil {
		t.Fatalf("payment contracts are not a distinct set: %v", err)
	}
}

func TestPaymentContractFixturesRunTheirSchemaCases(t *testing.T) {
	for _, f := range paymentFixtures {
		t.Run(f.name, func(t *testing.T) {
			cases := loadPaymentCases(t, f.name)
			report, err := EvaluateTechnicalSchemaFixtures(fixtureContract(t, "technical-"+f.name+".json"), cases)
			if err != nil {
				t.Fatal(err)
			}
			if !report.SchemaOnly || !report.AllPassed() || len(report.Results) != len(cases) {
				t.Fatalf("fixture report must be complete and schema-only: %+v", report)
			}
		})
	}
}

// Every variable a process holds is in the event log for good. A contract that
// let a card number through would put Atlas in PCI DSS scope on its first run.
func TestPaymentContractsRefuseCardData(t *testing.T) {
	cardFields := map[string]any{
		"cardNumber": "4242424242424242",
		"pan":        "4242424242424242",
		"cvv":        "123",
		"cvc":        "123",
		"expiry":     "12/30",
		"card":       map[string]any{"number": "4242424242424242"},
	}
	for _, f := range paymentFixtures {
		t.Run(f.name, func(t *testing.T) {
			compiled, err := CompileTechnicalContract(fixtureContract(t, "technical-"+f.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			cases := loadPaymentCases(t, f.name)
			for _, direction := range []string{"input", "output"} {
				for field, value := range cardFields {
					doc := firstValid(t, cases, direction)
					if err := validate(t, compiled, direction, doc); err != nil {
						t.Fatalf("%s baseline must be valid: %v", direction, err)
					}
					doc[field] = value
					if err := validate(t, compiled, direction, doc); err == nil {
						t.Errorf("%s accepted card field %q", direction, field)
					}
				}
			}
		})
	}
}

// A job runs at least once (ADR-0007, ADR-0299 gate 3). Without a key the
// provider can recognise, a retried capture or refund is a second booking.
func TestPaymentContractsThatMoveMoneyRequireAnIdempotencyKey(t *testing.T) {
	for _, f := range paymentFixtures {
		t.Run(f.name, func(t *testing.T) {
			c := fixtureContract(t, "technical-"+f.name+".json")
			compiled, err := CompileTechnicalContract(c)
			if err != nil {
				t.Fatal(err)
			}
			doc := firstValid(t, loadPaymentCases(t, f.name), "input")
			if !f.movesMoney {
				if _, has := doc["idempotencyKey"]; has {
					t.Fatalf("%s moves no money and should not ask for a key", f.name)
				}
				if c.Spec.Idempotency == "non-idempotent" {
					t.Fatalf("%s must be safe to repeat, got %q", f.name, c.Spec.Idempotency)
				}
				return
			}
			if c.Spec.Idempotency != "conditional" {
				t.Fatalf("idempotency %q, want conditional on the key", c.Spec.Idempotency)
			}
			delete(doc, "idempotencyKey")
			if err := validate(t, compiled, "input", doc); err == nil {
				t.Fatal("input without idempotencyKey accepted")
			}
			doc = firstValid(t, loadPaymentCases(t, f.name), "input")
			doc["idempotencyKey"] = "short"
			if err := validate(t, compiled, "input", doc); err == nil {
				t.Fatal("a key too short to be unique per instance and element was accepted")
			}
		})
	}
}

func TestPaymentAmountsAreWholeMinorUnitsInAnISOCurrency(t *testing.T) {
	bad := map[string]map[string]any{
		"fractional amount": {"amount": 12.5},
		"zero amount":       {"amount": 0},
		"negative amount":   {"amount": -100},
		"amount as string":  {"amount": "1000"},
		"lower-case code":   {"currency": "chf"},
		"four-letter code":  {"currency": "CHFX"},
	}
	for _, f := range paymentFixtures {
		cases := loadPaymentCases(t, f.name)
		if _, has := firstValid(t, cases, "input")["amount"]; !has {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			compiled, err := CompileTechnicalContract(fixtureContract(t, "technical-"+f.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			for label, patch := range bad {
				doc := firstValid(t, cases, "input")
				for k, v := range patch {
					doc[k] = v
				}
				if err := validate(t, compiled, "input", doc); err == nil {
					t.Errorf("%s accepted", label)
				}
			}
		})
	}
}

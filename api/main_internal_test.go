package api

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain hashes the test binary's passwords at bcrypt's minimum cost. A test's
// password protects nothing, and at the production cost the hashing alone was most
// of the package's run under the race detector: about 1,800 hashes and checks at
// over a second each. Everything else about a password — the length floor, the
// digest format, the comparison — is the code a server runs.
//
// It also builds the package's servers without the rule that everything belongs to an
// application (ADR-0544): the engine behaviour
// most of these tests cover is not about filing, and hundreds of them deploy a bare
// model. The rule's own tests turn it back on with withApplicationRequired, and
// TestAServerRequiresAnApplicationByDefault holds the production value.
func TestMain(m *testing.M) {
	passwordCost = bcrypt.MinCost
	applicationRequiredByDefault = false
	os.Exit(m.Run())
}

// TestAServerHashesAtTheDefaultCost holds the production cost where it is: lowering
// it for the tests above must never lower it for a server.
func TestAServerHashesAtTheDefaultCost(t *testing.T) {
	if productionPasswordCost != bcrypt.DefaultCost {
		t.Fatalf("productionPasswordCost = %d, want bcrypt.DefaultCost (%d)", productionPasswordCost, bcrypt.DefaultCost)
	}
	hash, err := hashPassword("a-password-that-is-long")
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != passwordCost {
		t.Fatalf("a hash carries cost %d (%v), want the cost it was asked for, %d", cost, err, passwordCost)
	}
	if !checkPassword(hash, "a-password-that-is-long") || checkPassword(hash, "another-password-that-is-long") {
		t.Fatal("a password checks against its own hash only")
	}
}

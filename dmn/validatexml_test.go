package dmn_test

import (
	"context"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestValidateXML checks a model already in hand (the upload path): a valid model
// reports its name and decisions; a non-compiling one reports invalid with a
// message.
func TestValidateXML(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})

	ok := v.ValidateXML(context.Background(), []byte(dishModel))
	if !ok.Resolved || !ok.Valid {
		t.Fatalf("valid model = %+v, want resolved+valid", ok)
	}
	if len(ok.Decisions) != 1 || ok.Decisions[0] != "Dish" {
		t.Errorf("decisions = %v, want [Dish]", ok.Decisions)
	}

	bad := v.ValidateXML(context.Background(), []byte("<not-dmn"))
	if bad.Valid || bad.Message == "" {
		t.Errorf("junk = %+v, want invalid with a message", bad)
	}
}

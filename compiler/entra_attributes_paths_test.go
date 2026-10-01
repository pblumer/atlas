package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// TestEntraInlineAttributesKeepEveryLiteralKind: null, false and a number are values a
// directory property legitimately takes — clearing a manager, disabling an account,
// a quota — and each must reach the request body as itself, inside lists too.
func TestEntraInlineAttributesKeepEveryLiteralKind(t *testing.T) {
	got := evalAttrs(t, `{"accountEnabled": false, "manager": null, "quota": 3.5, "tags": ["staff", "=team"]}`,
		map[string]expr.Value{"team": expr.String("ops")})
	if got["accountEnabled"] != false {
		t.Errorf("accountEnabled = %#v, want false", got["accountEnabled"])
	}
	if v, present := got["manager"]; !present || v != nil {
		t.Errorf("manager = %#v (present %v), want an explicit null", v, present)
	}
	if got["quota"] != 3.5 {
		t.Errorf("quota = %#v, want 3.5", got["quota"])
	}
	if tags, _ := got["tags"].([]any); len(tags) != 2 || tags[0] != "staff" || tags[1] != "ops" {
		t.Errorf("tags = %#v, want [staff ops]", got["tags"])
	}
}

// TestAnEmptyExpressionInsideAListIsRefused: the refusal for a value of just "=" holds
// at any depth, so a list cannot smuggle one through to a body nobody wrote.
func TestAnEmptyExpressionInsideAListIsRefused(t *testing.T) {
	_, err := entraAttributesExpr(strictFEEL, "createUser", `{"proxyAddresses": ["smtp:a@x.ch", "="]}`)
	if err == nil || !strings.Contains(err.Error(), `entra task "createUser" attributes: a value has an empty =expression`) {
		t.Fatalf("entraAttributesExpr = %v, want the empty expression refused", err)
	}
}

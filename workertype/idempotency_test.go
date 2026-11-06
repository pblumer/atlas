package workertype

import (
	"strings"
	"testing"
)

// Every operation declares what a second delivery of one of its jobs does, and the
// declaration grades the warning (ADR-0538).
func TestEveryOperationDeclaresItsIdempotencyAndEffect(t *testing.T) {
	for _, tc := range []struct {
		name, idempotency, effect, want string
	}{
		{"no idempotency", "", EffectReversible, "must declare idempotency"},
		{"unknown idempotency", "maybe", EffectReversible, "must be natural, key, check or none"},
		{"no effect", IdempotencyNone, "", "must declare effect"},
		{"unknown effect", IdempotencyNone, "sometimes", "must be reversible or irreversible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m.Spec.Operations[0].Idempotency, m.Spec.Operations[0].Effect = tc.idempotency, tc.effect
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want an error naming %q", err, tc.want)
			}
		})
	}
	for _, i := range []string{IdempotencyNatural, IdempotencyKey, IdempotencyCheck, IdempotencyNone} {
		for _, e := range []string{EffectReversible, EffectIrreversible} {
			m := validManifest()
			m.Spec.Operations[0].Idempotency, m.Spec.Operations[0].Effect = i, e
			if err := m.Validate(); err != nil {
				t.Errorf("%s/%s: %v", i, e, err)
			}
		}
	}
}

func TestDuplicateRisk(t *testing.T) {
	for _, tc := range []struct {
		idempotency, effect, want string
	}{
		{IdempotencyNatural, EffectIrreversible, ""},
		{IdempotencyKey, EffectIrreversible, ""},
		{IdempotencyCheck, EffectIrreversible, ""},
		{IdempotencyNone, EffectReversible, "notice"},
		{IdempotencyNone, EffectIrreversible, "warning"},
	} {
		if got := (Operation{Idempotency: tc.idempotency, Effect: tc.effect}).DuplicateRisk(); got != tc.want {
			t.Errorf("DuplicateRisk(%s, %s) = %q, want %q", tc.idempotency, tc.effect, got, tc.want)
		}
	}
}

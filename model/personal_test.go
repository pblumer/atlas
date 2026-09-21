package model

import (
	"strings"
	"testing"
)

// The one thing the engine can ask about a sealed value without a key (ADR-0314).
//
// Recognising an envelope is what lets the single writer refuse a declared personal value
// that arrived in the clear — a check it could not make if it had to decrypt anything, since
// it holds no key and applyToState must stay free of side effects (I4). The marker lives
// here, beside the variable's own encoding, because it is part of what a stored variable can
// be; the sealing lives in the vault, which builds its envelopes against this same constant.
func TestIsEncipheredRecognisesAnEnvelopeAndNothingElse(t *testing.T) {
	sealed := VariableValue{
		Name: "vorname",
		Kind: VarJSON,
		Text: EncipheredMarker + `{"subject":"P-4711","kind":3,"nonce":"AAAA","value":"AAAA"}}`,
	}
	if !sealed.IsEnciphered() {
		t.Errorf("a sealed value is not recognised: %q", sealed.Text)
	}
	for _, v := range []VariableValue{
		{Name: "vorname", Kind: VarString, Text: "Ida"},
		{Name: "vorname", Kind: VarNull},
		{Name: "vorname", Kind: VarBool, Bool: true},
		{Name: "betrag", Kind: VarNumber, Text: "12"},
		// The same text under a kind an envelope never has: the check is on both, because a
		// string whose *contents* look like an envelope is a string.
		{Name: "vorname", Kind: VarString, Text: sealed.Text},
		{Name: "profil", Kind: VarJSON, Text: `{"kuerzel":"ib"}`},
	} {
		if v.IsEnciphered() {
			t.Errorf("value of kind %d %q is taken for an envelope", v.Kind, v.Text)
		}
	}
	// The marker has to be a JSON object's opening, or a value carrying it would not be
	// valid JSON — which every other reader of a VarJSON variable assumes.
	if !strings.HasPrefix(EncipheredMarker, `{"`) {
		t.Errorf("EncipheredMarker %q does not open a JSON object", EncipheredMarker)
	}
}

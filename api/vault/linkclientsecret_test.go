package vault

import "testing"

// A provider's linking client secret lives under a reserved name, which no operator
// surface may read or write.
func TestALinkingClientSecretNameIsReserved(t *testing.T) {
	name := LinkClientSecretName("entra")
	if !IsLinkClientSecretName(name) || !IsReservedName(name) {
		t.Fatalf("%q is not recognised as a reserved linking client secret", name)
	}
	if IsLinkClientSecretName("entra") || IsReservedName("entra") {
		t.Fatal("the bare provider name is taken for a reserved one")
	}
}

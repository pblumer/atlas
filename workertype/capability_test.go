package workertype

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestOperationCapabilityClaimsRoundTrip(t *testing.T) {
	m := validManifest()
	m.Spec.Operations[0].Capabilities = []CapabilityClaim{
		{ID: "io.atlas.directory.entry.create", Version: "1.0.0", Profile: "ldap-entry"},
		{ID: "io.atlas.directory.entry.read", Version: "1.0.0"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON() error = %v", err)
	}
	claims := got.Spec.Operations[0].Capabilities
	if len(claims) != 2 || claims[0].ID != "io.atlas.directory.entry.create" ||
		claims[0].Version != "1.0.0" || claims[0].Profile != "ldap-entry" ||
		claims[1].ID != "io.atlas.directory.entry.read" {
		t.Fatalf("unexpected capability claims: %+v", claims)
	}
}

func TestManifestCapabilityClaimsRemainOptional(t *testing.T) {
	m := validManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("legacy manifest must still validate: %v", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"capabilities\"") {
		t.Fatalf("legacy manifest should not gain capabilities: %s", data)
	}
}

func TestManifestRejectsMalformedCapabilityClaims(t *testing.T) {
	tests := []struct {
		name string
		list []CapabilityClaim
		want string
	}{
		{"missing id", []CapabilityClaim{{Version: "1.0.0"}}, "capabilities"},
		{"invalid id", []CapabilityClaim{{ID: "user create", Version: "1.0.0"}}, "capabilities"},
		{"invalid version", []CapabilityClaim{{ID: "io.atlas.directory.user.create", Version: "latest"}}, "version"},
		{"missing version", []CapabilityClaim{{ID: "io.atlas.directory.user.create"}}, "version"},
		{"whitespace profile", []CapabilityClaim{{ID: "io.atlas.directory.user.create", Version: "1.0.0", Profile: "  "}}, "profile"},
		{"duplicate claim", []CapabilityClaim{
			{ID: "io.atlas.directory.user.create", Version: "1.0.0"},
			{ID: "io.atlas.directory.user.create", Version: "1.0.0", Profile: "other"},
		}, "duplicate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m.Spec.Operations[0].Capabilities = tc.list
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCapabilityClaimsDoNotChangeWorkerCompatibility(t *testing.T) {
	m := validManifest()
	m.Spec.Operations[0].Capabilities = []CapabilityClaim{
		{ID: "io.atlas.directory.entry.create", Version: "1.0.0"},
	}
	got, err := m.CompatibleWith("1.2.3")
	if err != nil || !got {
		t.Fatalf("CompatibleWith() = %v, %v; capability metadata must not affect compatibility", got, err)
	}
}

// TestValidateCapabilityClaimsIsTheManifestRule pins that the exported check a
// built-in Worker Type's operation table is held to is the one a manifest is
// admitted by, not a second copy that could drift from it (ADR-0480).
func TestValidateCapabilityClaimsIsTheManifestRule(t *testing.T) {
	ok := []CapabilityClaim{{ID: "io.atlas.collaboration.channel.create", Version: "0.1.0", Profile: "microsoft-teams"}}
	if err := ValidateCapabilityClaims("create-channel", ok); err != nil {
		t.Fatalf("ValidateCapabilityClaims(valid) = %v", err)
	}
	bad := []CapabilityClaim{{ID: "Collaboration Channel", Version: "0.1.0"}}
	err := ValidateCapabilityClaims("create-channel", bad)
	if err == nil || !strings.Contains(err.Error(), `"create-channel"`) {
		t.Fatalf("ValidateCapabilityClaims(invalid) = %v, want an error naming the operation", err)
	}
	m := validManifest()
	m.Spec.Operations[0].Capabilities = bad
	if merr := m.Validate(); merr == nil || merr.Error() != strings.Replace(err.Error(), `"create-channel"`, `"`+m.Spec.Operations[0].ID+`"`, 1) {
		t.Fatalf("manifest refusal %v differs from ValidateCapabilityClaims' %v", merr, err)
	}
}

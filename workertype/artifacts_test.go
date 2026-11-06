package workertype

import (
	"strings"
	"testing"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A manifest names its executables per platform, and ArtifactFor finds the one for a
// platform in either form (ADR-0535 §1).
func TestArtifactsPerPlatform(t *testing.T) {
	m := validManifest()
	m.Spec.Artifact = nil
	m.Spec.Artifacts = []Artifact{
		{Platform: "linux/amd64", MediaType: MediaTypeExecutable, Digest: testDigest},
		{Platform: "windows/amd64", MediaType: MediaTypeExecutable, Digest: testDigest},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a, ok := m.ArtifactFor("windows/amd64"); !ok || a.Platform != "windows/amd64" {
		t.Errorf("ArtifactFor(windows/amd64) = %+v, %v", a, ok)
	}
	if _, ok := m.ArtifactFor("darwin/arm64"); ok {
		t.Error("ArtifactFor a platform it does not list = found")
	}
	single := validManifest()
	if a, ok := single.ArtifactFor("linux/amd64"); !ok || a.Digest != single.Spec.Artifact.Digest {
		t.Errorf("ArtifactFor on the single-artifact form = %+v, %v", a, ok)
	}
}

func TestArtifactsAreValidated(t *testing.T) {
	good := Artifact{Platform: "linux/amd64", MediaType: MediaTypeExecutable, Digest: testDigest}
	for _, tc := range []struct {
		name      string
		single    *Artifact
		artifacts []Artifact
		want      string
	}{
		{"both forms", &good, []Artifact{good}, "exclusive"},
		{"neither for an external runtime", nil, nil, "is required for an external runtime"},
		{"a platform twice", nil, []Artifact{good, good}, "twice"},
		{"no platform", nil, []Artifact{{MediaType: MediaTypeExecutable, Digest: testDigest}}, "platform is required"},
		{"not os/arch", nil, []Artifact{{Platform: "linux", MediaType: MediaTypeExecutable, Digest: testDigest}}, "os/arch"},
		{"no media type", nil, []Artifact{{Platform: "linux/amd64", Digest: testDigest}}, "mediaType is required"},
		{"bad digest", nil, []Artifact{{Platform: "linux/amd64", MediaType: MediaTypeExecutable, Digest: "sha1:x"}}, "digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m.Spec.Artifact, m.Spec.Artifacts = tc.single, tc.artifacts
			if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

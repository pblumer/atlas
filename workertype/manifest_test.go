package workertype

import (
	"strings"
	"testing"
)

func TestParseManifestAcceptsValidExternalWorkerType(t *testing.T) {
	m, err := ParseJSON([]byte(`{
		"apiVersion":"atlas.io/v1alpha1",
		"kind":"WorkerType",
		"metadata":{"id":"com.example.sap-s4","version":"2.1.3","name":"SAP S/4HANA"},
		"spec":{
			"atlasCompatibility":">=1.0.0 <2.0.0",
			"workerProtocol":"v1",
			"runtime":{"mode":"external"},
			"jobTypes":["io.example.sap-s4"],
			"operations":[{"id":"create-business-partner","jobType":"io.example.sap-s4"}],
			"configurationSchema":"config.schema.json",
			"modelerTemplate":"template.json",
			"artifact":{
				"platform":"linux/amd64",
				"mediaType":"application/vnd.oci.image.manifest.v1+json",
				"digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseJSON() error = %v", err)
	}
	if m.Metadata.ID != "com.example.sap-s4" || m.Metadata.Version != "2.1.3" {
		t.Fatalf("unexpected identity: %+v", m.Metadata)
	}
	if m.Spec.Artifact == nil || m.Spec.Artifact.Digest == "" {
		t.Fatalf("artifact was not parsed: %+v", m.Spec.Artifact)
	}
}

func TestManifestValidateRejectsInvalidContracts(t *testing.T) {
	valid := Manifest{
		APIVersion: APIVersionV1Alpha1,
		Kind:       KindWorkerType,
		Metadata: Metadata{ID: "com.example.mail", Version: "1.2.3", Name: "Mail"},
		Spec: Spec{
			AtlasCompatibility: ">=1.0.0 <2.0.0",
			WorkerProtocol:     WorkerProtocolV1,
			Runtime:            Runtime{Mode: RuntimeExternal},
			JobTypes:           []string{"io.example.mail"},
			Operations:         []Operation{{ID: "send", JobType: "io.example.mail"}},
			ConfigurationSchema: "config.schema.json",
			ModelerTemplate:     "template.json",
			Artifact: &Artifact{
				Platform:  "linux/amd64",
				MediaType: "application/vnd.oci.image.manifest.v1+json",
				Digest:    "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		},
	}

	tests := []struct {
		name string
		edit func(*Manifest)
		want string
	}{
		{"api version", func(m *Manifest) { m.APIVersion = "atlas.io/v2" }, "apiVersion"},
		{"kind", func(m *Manifest) { m.Kind = "Plugin" }, "kind"},
		{"id", func(m *Manifest) { m.Metadata.ID = "SAP Worker" }, "metadata.id"},
		{"version", func(m *Manifest) { m.Metadata.Version = "latest" }, "metadata.version"},
		{"compatibility", func(m *Manifest) { m.Spec.AtlasCompatibility = ">=one" }, "atlasCompatibility"},
		{"protocol", func(m *Manifest) { m.Spec.WorkerProtocol = "v9" }, "workerProtocol"},
		{"runtime", func(m *Manifest) { m.Spec.Runtime.Mode = "shell" }, "runtime.mode"},
		{"missing artifact", func(m *Manifest) { m.Spec.Artifact = nil }, "artifact"},
		{"digest", func(m *Manifest) { m.Spec.Artifact.Digest = "sha256:not-a-digest" }, "artifact.digest"},
		{"duplicate job type", func(m *Manifest) { m.Spec.JobTypes = append(m.Spec.JobTypes, "io.example.mail") }, "jobTypes"},
		{"operation unknown job type", func(m *Manifest) { m.Spec.Operations[0].JobType = "io.example.other" }, "operations"},
		{"duplicate operation", func(m *Manifest) { m.Spec.Operations = append(m.Spec.Operations, m.Spec.Operations[0]) }, "operations"},
		{"configuration schema", func(m *Manifest) { m.Spec.ConfigurationSchema = "" }, "configurationSchema"},
		{"modeler template", func(m *Manifest) { m.Spec.ModelerTemplate = "" }, "modelerTemplate"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := valid
			m.Spec.JobTypes = append([]string(nil), valid.Spec.JobTypes...)
			m.Spec.Operations = append([]Operation(nil), valid.Spec.Operations...)
			if valid.Spec.Artifact != nil {
				a := *valid.Spec.Artifact
				m.Spec.Artifact = &a
			}
			tc.edit(&m)
			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestManifestCompatibility(t *testing.T) {
	m := Manifest{Spec: Spec{AtlasCompatibility: ">=1.2.0 <2.0.0"}}
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"1.2.0", true},
		{"1.9.7", true},
		{"1.1.9", false},
		{"2.0.0", false},
	} {
		got, err := m.CompatibleWith(tc.version)
		if err != nil {
			t.Fatalf("CompatibleWith(%q) error = %v", tc.version, err)
		}
		if got != tc.want {
			t.Fatalf("CompatibleWith(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestParseManifestRejectsUnknownFields(t *testing.T) {
	_, err := ParseJSON([]byte(`{"apiVersion":"atlas.io/v1alpha1","kind":"WorkerType","metadata":{"id":"com.example.mail","version":"1.0.0","name":"Mail"},"spec":{},"command":"rm -rf /"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseJSON() error = %v, want unknown-field error", err)
	}
}

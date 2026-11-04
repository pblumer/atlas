package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/capability"
)

// TestATreeWhoseArchitectureDoesNotHoldTogetherIsRefusedWithTheReason walks the
// refusals of treeArchitecture: each case is a tree that names something it does not
// carry or says something twice, and the refusal names what is wrong.
func TestATreeWhoseArchitectureDoesNotHoldTogetherIsRefusedWithTheReason(t *testing.T) {
	const model = `{"name":"Orders","classes":[{"name":"Order","stereotype":"businessObject"}]}`
	const cap1 = `{"key":"konten","name":"Konten","scope":"Accounts.","owner":{"name":"IM"}}`
	procs := []sourceProcess{{ID: "p1", Name: "P", Path: "p1.bpmn"}}
	cases := []struct {
		name  string
		man   sourceManifest
		files map[string]string
		want  string
	}{
		{"an older format", sourceManifest{FormatVersion: 3, Architecture: "a.json"}, nil, "carries; it says format 3"},
		{"a missing model file",
			sourceManifest{FormatVersion: 4, InformationModels: []sourceInfoModel{{Name: "Orders", Path: "m.json"}}}, nil, "not in the tree"},
		{"an unreadable model",
			sourceManifest{FormatVersion: 4, InformationModels: []sourceInfoModel{{Name: "Orders", Path: "m.json"}}},
			map[string]string{"m.json": "nope"}, "information model m.json"},
		{"a model named otherwise",
			sourceManifest{FormatVersion: 4, InformationModels: []sourceInfoModel{{Name: "Billing", Path: "m.json"}}},
			map[string]string{"m.json": model}, `names it "Billing"`},
		{"two models of one name",
			sourceManifest{FormatVersion: 4, InformationModels: []sourceInfoModel{{Path: "m.json"}, {Path: "n.json"}}},
			map[string]string{"m.json": model, "n.json": model}, "two information models"},
		{"a missing architecture file", sourceManifest{FormatVersion: 4, Architecture: "a.json"}, nil, "not in the tree"},
		{"an unknown field", sourceManifest{FormatVersion: 4, Architecture: "a.json"},
			map[string]string{"a.json": `{"surprise":1}`}, "invalid a.json"},
		{"a realization of a foreign process", sourceManifest{FormatVersion: 4, Architecture: "a.json", Processes: procs},
			map[string]string{"a.json": `{"realizations":[{"capability":"konten","kind":"process","processId":"elsewhere"}]}`}, "not a process of this application"},
		{"a proposal with realizations", sourceManifest{FormatVersion: 4, Architecture: "a.json"},
			map[string]string{"a.json": `{"realizations":[],"capabilities":[{"key":"konten","name":"K","realizations":[{"kind":"manual","note":"x"}]}]}`}, "carries realizations"},
		{"an invalid proposal", sourceManifest{FormatVersion: 4, Architecture: "a.json"},
			map[string]string{"a.json": `{"realizations":[],"capabilities":[{"key":"Bad Key!"}]}`}, "proposed capability"},
		{"a capability proposed twice", sourceManifest{FormatVersion: 4, Architecture: "a.json"},
			map[string]string{"a.json": `{"realizations":[],"capabilities":[` + cap1 + `,` + cap1 + `]}`}, "twice"},
		{"an invalid value stream", sourceManifest{FormatVersion: 4, Architecture: "a.json"},
			map[string]string{"a.json": `{"realizations":[],"capabilities":[],"valueStreams":[{"key":"Bad Key!"}]}`}, "proposed value stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			byPath := map[string][]byte{}
			for p, d := range tc.files {
				byPath[p] = []byte(d)
			}
			_, err := treeArchitecture(tc.man, byPath)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestArchitectureIsOnlyAddedToATreeThatStartsWithItsManifest: the exporter writes the
// architecture into the manifest, so a tree without one in front is an error, not a
// tree with a second manifest.
func TestArchitectureIsOnlyAddedToATreeThatStartsWithItsManifest(t *testing.T) {
	arch := sourceArchitectureRecords{realizations: nil}
	if out, err := addSourceArchitecture(nil, arch); err != nil || out != nil {
		t.Fatalf("an application without architecture changed the tree: %v %v", out, err)
	}
	arch.realizations = &capability.ApplicationRealizations{}
	if _, err := addSourceArchitecture([]sourceFile{{Path: "other.json"}}, arch); err == nil {
		t.Error("a tree without a manifest in front was accepted")
	}
	if _, err := addSourceArchitecture([]sourceFile{{Path: manifestPath, Data: []byte("nope")}}, arch); err == nil {
		t.Error("an unreadable manifest was accepted")
	}
}

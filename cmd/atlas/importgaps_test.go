package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/importplan"
)

// A package carries its application's information model and what it brings to the
// capability map (ADR-draft-application-bundle-architecture). A key the server's map
// lacks is a gap: in a pipeline the import refuses until a flag decides it, and the
// flags decide it the way the server does.
func TestImportDecidesTheCapabilityMapsGaps(t *testing.T) {
	ts, _ := liveServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "approval.bpmn", strings.Replace(scenarioBPMN, `id="approval"`, `id="arch-approval"`, 1))
	writeFile(t, dir, "model.json", `{"name":"Antraege","classes":[{"id":"cls-antrag","name":"Antrag","stereotype":"businessObject","attributes":[]}],"associations":[],"stores":[]}`)
	writeFile(t, dir, "arch.json", `{"realizations":[{"capability":"antraege","kind":"process","processId":"arch-approval"}],
	  "capabilities":[{"key":"antraege","name":"Antraege genehmigen","state":"active"}],"valueStreams":[]}`)
	writeFile(t, dir, packageManifest, `{"formatVersion":4,"key":"antraege","name":"Antraege",
	  "processes":[{"id":"arch-approval","name":"Approval","path":"approval.bpmn"}],"forms":[],"decisions":[],
	  "informationModels":[{"name":"Antraege","path":"model.json"}],"architecture":"arch.json"}`)

	var out bytes.Buffer
	err := runImport([]string{"--server", ts.URL, dir}, &out)
	if err == nil || !strings.Contains(err.Error(), "capability:antraege") || !strings.Contains(err.Error(), "--omit all") {
		t.Fatalf("an undecided gap: err=%v\n%s", err, out.String())
	}
	if code := capabilityStatus(t, ts.URL, "antraege"); code != http.StatusNotFound {
		t.Fatalf("a refused import filed the capability: %d", code)
	}

	out.Reset()
	if err := runImport([]string{"--server", ts.URL, "--adopt", "capability:antraege", dir}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "gap") || !strings.Contains(out.String(), "proposal is filed in the map") {
		t.Errorf("the plan does not show the gap:\n%s", out.String())
	}
	if code := capabilityStatus(t, ts.URL, "antraege"); code != http.StatusOK {
		t.Fatalf("the adopted capability: %d", code)
	}
}

func gapArtifact(ref string, proposed bool) importplan.Artifact {
	return importplan.Artifact{Ref: ref, Status: importplan.Gap, Outcome: importplan.Undecided, Proposed: proposed}
}

func capabilityStatus(t *testing.T, server, key string) int {
	t.Helper()
	res, err := http.Get(server + "/api/v1/capabilities/" + key)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func TestTheGapFlagsReachTheServer(t *testing.T) {
	adopt, omit := newRefFlag(), newRefFlag()
	_ = adopt.Set("capability:a")
	_ = omit.Set("valuestream:v")
	d, err := decisionsWithGaps(newRefFlag(), newRefFlag(), adopt, omit, []string{"capability:b=identitaet"})
	if err != nil {
		t.Fatal(err)
	}
	p := importPlan{artifacts: []planned{
		{Artifact: gapArtifact("capability:a", true)},
		{Artifact: gapArtifact("capability:b", false)},
		{Artifact: gapArtifact("valuestream:v", true)},
	}}
	p.decide(d)
	q := p.query()
	for _, want := range []string{"adopt=capability%3Aa", "map=capability%3Ab%3Didentitaet", "omit=valuestream%3Av"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
	if _, err := decisionsWithGaps(newRefFlag(), newRefFlag(), adopt, newRefFlag(), []string{"capability:a=x"}); err == nil {
		t.Fatal("a gap both adopted and mapped was accepted")
	}
	if _, err := decisionsWithGaps(newRefFlag(), newRefFlag(), newRefFlag(), newRefFlag(), []string{"capability:a"}); err == nil {
		t.Fatal("--map without a key was accepted")
	}
	raw, _ := json.Marshal(p.artifacts[0].Artifact)
	if !strings.Contains(string(raw), `"proposed":true`) {
		t.Fatalf("artifact = %s", raw)
	}
}

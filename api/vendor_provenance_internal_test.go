package api

import (
	"crypto/sha1"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// gitBlobSHA1 records Git's content identity, not an independently verified
// redistribution licence. These hashes are checked against tagged upstream files.
func gitBlobSHA1(data []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// TestVendoredZeebeModdleProvenance keeps the imported descriptor identical to
// the MIT-licensed v1.17.0 upstream file. A future modification must be traced
// and reviewed, rather than quietly presented as an unchanged upstream asset.
func TestVendoredZeebeModdleProvenance(t *testing.T) {
	const descriptor = "web/vendor/bpmn/zeebe.json"
	const upstreamGitBlob = "1a73ad8f64c6a3c6a2321f8765062ba429f3a38d"
	const notice = "web/vendor/bpmn/ZEEBE-BPMN-MODDLE-LICENSE"
	const upstreamLicenceGitBlob = "f6d5667d451a6ecc20e941e8a26f5781933f9c7f"

	data, err := fs.ReadFile(webFS, descriptor)
	if err != nil {
		t.Fatalf("read %s: %v", descriptor, err)
	}
	if got := gitBlobSHA1(data); got != upstreamGitBlob {
		t.Errorf("%s differs from camunda/zeebe-bpmn-moddle v1.17.0 (git blob %s, want %s); review provenance and update the evidence, not just the hash", descriptor, got, upstreamGitBlob)
	}
	licence, err := fs.ReadFile(webFS, notice)
	if err != nil {
		t.Fatalf("MIT copyright and permission notice for %s is absent: %v", descriptor, err)
	}
	if got := gitBlobSHA1(licence); got != upstreamLicenceGitBlob {
		t.Errorf("%s differs from the v1.17.0 upstream MIT licence (git blob %s, want %s)", notice, got, upstreamLicenceGitBlob)
	}
	recipe, err := fs.ReadFile(webFS, "web/vendor/bpmn/README.md")
	if err != nil {
		t.Fatalf("read BPMN vendor provenance: %v", err)
	}
	if !strings.Contains(string(recipe), "zeebe-bpmn-moddle 1.17.0") ||
		!strings.Contains(string(recipe), "ZEEBE-BPMN-MODDLE-LICENSE") {
		t.Error("BPMN vendor manifest must pin the exact descriptor version and its own MIT licence")
	}
}

// TestVendoredCamundaDMNModdleNotice checks the independently licensed descriptor
// bundled in the DMN Modeler. The separate bundle checksum test covers the built
// artifact; this guard covers its explicit upstream licence and dependency record.
func TestVendoredCamundaDMNModdleNotice(t *testing.T) {
	const notice = "web/vendor/dmn/CAMUNDA-DMN-MODDLE-LICENSE"
	const upstreamLicenceGitBlob = "49c025e38d8340f3e30d24111e7ad3f071d71b5e"

	licence, err := fs.ReadFile(webFS, notice)
	if err != nil {
		t.Fatalf("MIT copyright and permission notice for camunda-dmn-moddle is absent: %v", err)
	}
	if got := gitBlobSHA1(licence); got != upstreamLicenceGitBlob {
		t.Errorf("%s differs from camunda-dmn-moddle v1.3.1 MIT licence (git blob %s, want %s)", notice, got, upstreamLicenceGitBlob)
	}
	manifest, err := fs.ReadFile(webFS, "web/vendor/dmn/ATLAS-VENDORED.txt")
	if err != nil {
		t.Fatalf("read DMN vendor provenance: %v", err)
	}
	if !strings.Contains(string(manifest), "camunda-dmn-moddle@1.3.1") ||
		!strings.Contains(string(manifest), "CAMUNDA-DMN-MODDLE-LICENSE") {
		t.Error("DMN vendor manifest must pin the extension's exact upstream version and MIT notice")
	}
}

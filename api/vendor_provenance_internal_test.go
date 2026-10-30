package api

import (
	"crypto/sha1"
	"encoding/json"
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

// TestVendoredDMNForkLicences pins the separately copyrighted dmn-js and
// dmn-moddle fork notices to their recorded Git commits, not to each other.
// Even a license-only change must have its provenance reviewed.
func TestVendoredDMNForkLicences(t *testing.T) {
	const vendorManifest = "web/vendor/dmn/ATLAS-VENDORED.txt"
	const dmnJSNotice = "web/vendor/dmn/LICENSE"
	const dmnJSLicenceGitBlob = "c6786fe7f8afb91b456f3009ae5d69bb9e387402"
	const moddleNotice = "web/vendor/dmn/DMN-MODDLE-LICENSE"
	const moddleLicenceGitBlob = "e0a77fed0e410060b20cae9e1f2ec9eccc5618dc"

	for _, tc := range []struct {
		path string
		want string
	}{
		{dmnJSNotice, dmnJSLicenceGitBlob},
		{moddleNotice, moddleLicenceGitBlob},
	} {
		data, err := fs.ReadFile(webFS, tc.path)
		if err != nil {
			t.Errorf("missing original upstream licence %s: %v", tc.path, err)
			continue
		}
		if got := gitBlobSHA1(data); got != tc.want {
			t.Errorf("%s: Git blob SHA %s, want pinned upstream %s", tc.path, got, tc.want)
		}
	}

	manifest, err := fs.ReadFile(webFS, vendorManifest)
	if err != nil {
		t.Fatalf("read DMN vendor manifest: %v", err)
	}
	for _, want := range []string{
		"481cc5a02ffa855689b59d81181478b84b25f9a5",
		"ac76928df99382c36833d9300fd98acc604dc673",
		"DMN-MODDLE-LICENSE",
	} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("%s must record %s", vendorManifest, want)
		}
	}
}

// TestVendoredDMNLockfileLicenceInventory is intentionally strict: new
// dependencies require rechecking exact package sources, metadata and notices.
// A lockfile is build evidence, NOT proof that all these packages ship in JS.
func TestVendoredDMNLockfileLicenceInventory(t *testing.T) {
	data, err := fs.ReadFile(webFS, "web/vendor/dmn/src/package-lock.json")
	if err != nil {
		t.Fatalf("read DMN vendor npm lockfile: %v", err)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
			License string `json:"license"`
			Resolved string `json:"resolved"`
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("parse DMN vendor npm lockfile: %v", err)
	}
	const expectedPackages = 68
	total, mit, isc := 0, 0, 0
	for name, dep := range lock.Packages {
		if !strings.HasPrefix(name, "node_modules/") {
			continue
		}
		total++
		if dep.Version == "" || dep.Resolved == "" || dep.Integrity == "" {
			t.Errorf("%s is not pinned to a version, source URL and integrity digest", name)
		}
		switch dep.License {
		case "MIT":
			mit++
		case "ISC":
			isc++
		default:
			t.Errorf("%s has missing or unreviewed declared licence %q", name, dep.License)
		}
	}
	if total != expectedPackages || mit != 67 || isc != 1 {
		t.Errorf("DMN npm lockfile licence inventory changed: packages=%d MIT=%d ISC=%d, want %d/67/1; review before updating the evidence", total, mit, isc, expectedPackages)
	}
}

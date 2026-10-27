package blueprintregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

type packageTestEntry struct {
	name string
	data string
	kind byte
	link string
}

func packageFixture(t *testing.T, entries ...packageTestEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: entry.name, Mode: 0o644, Typeflag: kind, Linkname: entry.link}
		if kind == tar.TypeReg {
			h.Size = int64(len(entry.data))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tw.Write([]byte(entry.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func packageRelease(data []byte) Release {
	digest := sha256.Sum256(data)
	return Release{Artifact: Artifact{
		Path: "packages/example.tar.gz", SizeBytes: int64(len(data)),
		Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}}
}

func TestInspectPackageReadsValidExportWithoutInstalling(t *testing.T) {
	data := packageFixture(t,
		packageTestEntry{name: "atlas.json", data: `{"key":"purchase-approval","name":"Purchase approval","formatVersion":1,"processes":[],"forms":[],"decisions":[]}`},
		packageTestEntry{name: "processes/request.bpmn", data: "<definitions />"},
		packageTestEntry{name: "katalog.json", data: `{"catalogs":[]}`},
	)
	inspection, err := InspectPackage(packageRelease(data), data)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.ApplicationKey != "purchase-approval" || inspection.ApplicationName != "Purchase approval" {
		t.Fatalf("unexpected source manifest: %+v", inspection)
	}
	if len(inspection.Files) != 3 || inspection.Files[0].Path != "atlas.json" || inspection.Files[1].SizeBytes != int64(len("<definitions />")) {
		t.Fatalf("wrong files: %+v", inspection.Files)
	}
	// Inspection returns only metadata; the existing import endpoint remains
	// the authority for BPMN compilation, conflict plans and publication.
	if inspection.Files[2].Path != "katalog.json" {
		t.Fatal("shop payload was dropped")
	}
}

func TestInspectPackageRejectsModifiedCompressedBytes(t *testing.T) {
	data := packageFixture(t, packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`})
	release := packageRelease(data)
	corrupted := append([]byte(nil), data...)
	corrupted[len(corrupted)-2] ^= 1
	if _, err := InspectPackage(release, corrupted); err == nil {
		t.Fatal("changed compressed bytes accepted")
	}
}

func TestInspectPackageRejectsDangerousHeadersAndPaths(t *testing.T) {
	cases := []struct {
		name  string
		entry packageTestEntry
	}{
		{"symlink", packageTestEntry{name: "processes/p.bpmn", kind: tar.TypeSymlink, link: "../../etc/passwd"}},
		{"hardlink", packageTestEntry{name: "processes/p.bpmn", kind: tar.TypeLink, link: "atlas.json"}},
		{"directory", packageTestEntry{name: "processes/", kind: tar.TypeDir}},
		{"absolute path", packageTestEntry{name: "/tmp/p.bpmn", data: "x"}},
		{"parent traversal", packageTestEntry{name: "../p.bpmn", data: "x"}},
		{"embedded traversal", packageTestEntry{name: "processes/../p.bpmn", data: "x"}},
		{"windows traversal", packageTestEntry{name: `processes\..\p.bpmn`, data: "x"}},
		{"windows drive", packageTestEntry{name: "C:/p.bpmn", data: "x"}},
		{"double slash", packageTestEntry{name: "processes//p.bpmn", data: "x"}},
		{"dot segment", packageTestEntry{name: "./p.bpmn", data: "x"}},
		{"percent escape", packageTestEntry{name: "processes/%2e%2e/p.bpmn", data: "x"}},
		{"control character", packageTestEntry{name: "processes/p\nbpmn", data: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := packageFixture(t,
				packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`},
				tc.entry,
			)
			if _, err := InspectPackage(packageRelease(data), data); err == nil {
				t.Fatalf("accepted prohibited tar entry %#v", tc.entry)
			}
		})
	}
}

func TestInspectPackageRejectsMalformedOrAmbiguousContents(t *testing.T) {
	cases := []struct {
		name    string
		entries []packageTestEntry
	}{
		{"missing manifest", []packageTestEntry{{name: "processes/p.bpmn", data: "a"}}},
		{"blank manifest", []packageTestEntry{{name: "atlas.json", data: ""}}},
		{"invalid JSON", []packageTestEntry{{name: "atlas.json", data: "{"}}},
		{"duplicate manifest field", []packageTestEntry{{name: "atlas.json", data: `{"key":"x","key":"y","name":"X"}`}}},
		{"blank key", []packageTestEntry{{name: "atlas.json", data: `{"key":"","name":"Example"}`}}},
		{"blank name", []packageTestEntry{{name: "atlas.json", data: `{"key":"x","name":""}`}}},
		{"duplicate manifest", []packageTestEntry{{name: "atlas.json", data: `{"key":"x","name":"X"}`}, {name: "atlas.json", data: `{"key":"y","name":"Y"}`}}},
		{"case-folded collision", []packageTestEntry{{name: "atlas.json", data: `{"key":"x","name":"X"}`}, {name: "ATLAS.JSON", data: "other"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := packageFixture(t, tc.entries...)
			if _, err := InspectPackage(packageRelease(data), data); err == nil {
				t.Fatal("malformed or ambiguous archive accepted")
			}
		})
	}
}

func TestInspectPackageRejectsTruncationAndAppendedCompressedData(t *testing.T) {
	raw := packageFixture(t, packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`})
	cases := [][]byte{
		[]byte("not a gzip archive"),
		raw[:len(raw)-5],
		append(append([]byte(nil), raw...), []byte("trailing-data")...),
		append(append([]byte(nil), raw...), packageFixture(t, packageTestEntry{name: "atlas.json", data: `{"key":"evil","name":"Y"}`})...),
	}
	for i, data := range cases {
		if _, err := InspectPackage(packageRelease(data), data); err == nil {
			t.Errorf("bad compressed archive case %d accepted", i)
		}
	}
}

func TestInspectPackageRejectsExpandedLimitsAndEntryLimits(t *testing.T) {
	data := packageFixture(t,
		packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`},
		packageTestEntry{name: "processes/a.bpmn", data: strings.Repeat("x", 2000)},
	)
	for _, limits := range []archiveLimits{
		{maxExpandedBytes: 1600, maxEntries: 2000},
		{maxExpandedBytes: MaxPackageExpandedBytes, maxEntries: 1},
	} {
		if _, err := inspectPackageWithLimits(packageRelease(data), data, limits); err == nil {
			t.Fatalf("accepted archive beyond limits: %+v", limits)
		}
	}
}

func TestInspectPackageRejectsIncompleteGzipCRC(t *testing.T) {
	data := packageFixture(t, packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`})
	trailer := append([]byte(nil), data...)
	trailer[len(trailer)-8] ^= 1
	if _, err := InspectPackage(packageRelease(trailer), trailer); err == nil {
		t.Fatal("invalid gzip checksum was accepted")
	}
}

func TestInspectPackageAcceptsBoundedValidArchive(t *testing.T) {
	data := packageFixture(t, packageTestEntry{name: "atlas.json", data: `{"key":"x","name":"X"}`})
	got, err := inspectPackageWithLimits(packageRelease(data), data, archiveLimits{maxExpandedBytes: 4096, maxEntries: 1})
	if err != nil || got.ApplicationKey != "x" {
		t.Fatalf("bounded normal tar failed: %#v / %v", got, err)
	}
}

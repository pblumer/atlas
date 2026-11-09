package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/workertype"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// manifestFor is a valid external Worker Type manifest whose artifact for platform
// has the given digest and media type.
func manifestFor(version, platform, digest, mediaType string) []byte {
	return []byte(fmt.Sprintf(`{"apiVersion":"atlas.io/v1alpha1","kind":"WorkerType",
"metadata":{"id":"com.example.echo","version":%q,"name":"Echo"},
"spec":{"atlasCompatibility":">=0.1.0","workerProtocol":"v1","runtime":{"mode":"external"},
"jobTypes":["com.example.echo"],"operations":[{"id":"echo","jobType":"com.example.echo","idempotency":"natural","effect":"reversible"}],
"configurationSchema":"config.schema.json","modelerTemplate":"template.json",
"artifacts":[{"platform":%q,"mediaType":%q,"digest":%q}]}}`, version, platform, mediaType, digest))
}

var now = time.Unix(1_800_000_000, 0)

func TestInstallVerifyListRemove(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := []byte("#!/bin/sh\necho hello\n")
	m := manifestFor("1.0.0", Platform(), digestOf(bin), workertype.MediaTypeExecutable)

	inst, err := s.Install(m, bytes.NewReader(bin), Platform(), now)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if inst.ID != "com.example.echo" || inst.Version != "1.0.0" || inst.Digest != digestOf(bin) || inst.Signed || inst.Origin != OriginFile {
		t.Errorf("installed = %+v", inst)
	}
	if got, err := os.ReadFile(inst.Path); err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("executable = %q, %v; want the artifact's bytes", got, err)
	}

	// The same release again changes nothing; other bytes under the same version are refused.
	if again, err := s.Install(m, bytes.NewReader(bin), Platform(), now.Add(time.Hour)); err != nil || again.InstalledAt != inst.InstalledAt {
		t.Errorf("re-install = %+v, %v; want the existing release unchanged", again, err)
	}
	other := []byte("other bytes")
	if _, err := s.Install(manifestFor("1.0.0", Platform(), digestOf(other), workertype.MediaTypeExecutable),
		bytes.NewReader(other), Platform(), now); !errors.Is(err, ErrConflict) {
		t.Errorf("another artifact under 1.0.0 = %v, want ErrConflict", err)
	}

	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Path != inst.Path {
		t.Fatalf("List = %+v, %v; want the one release", list, err)
	}
	if v, err := s.Verified("com.example.echo", "1.0.0"); err != nil || v.Path != inst.Path {
		t.Fatalf("Verified = %+v, %v", v, err)
	}

	// An executable altered on disk is refused before it could be run.
	if err := os.WriteFile(inst.Path, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verified("com.example.echo", "1.0.0"); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Errorf("Verified after tampering = %v, want a refusal", err)
	}

	if err := s.Remove("com.example.echo", "1.0.0"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("List after Remove = %+v", list)
	}
	if _, err := s.Verified("com.example.echo", "1.0.0"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Verified after Remove = %v, want not found", err)
	}
	if err := s.Remove("../escape", "1.0.0"); err == nil {
		t.Error("Remove with a path in the id = nil, want a refusal")
	}
}

func TestInstallRefusesWhatItCannotTrustOrRun(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := []byte("payload")
	for _, tc := range []struct {
		name     string
		manifest []byte
		platform string
		want     string
	}{
		{"digest mismatch", manifestFor("1.0.0", Platform(), digestOf([]byte("something else")), workertype.MediaTypeExecutable), Platform(), "nothing was installed"},
		{"no artifact for this platform", manifestFor("1.0.0", "plan9/mips", digestOf(bin), workertype.MediaTypeExecutable), Platform(), "no artifact for"},
		{"an OCI image", manifestFor("1.0.0", Platform(), digestOf(bin), "application/vnd.oci.image.manifest.v1+json"), Platform(), "launches only"},
		{"not a manifest", []byte(`{"kind":"WorkerType"}`), Platform(), ""},
		{"too large", bytes.Repeat([]byte("x"), MaxManifestBytes+1), Platform(), "larger than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Install(tc.manifest, bytes.NewReader(bin), tc.platform, now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Install = %v, want an error naming %q", err, tc.want)
			}
		})
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("a refused install left %+v", list)
	}
}

func TestAnInterruptedInstallIsNotListed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A release directory without its metadata is what a crash between the rename and
	// the metadata write leaves.
	if err := os.MkdirAll(dir+"/com.example.echo/1.0.0/abc", 0o700); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Errorf("List = %+v, %v; want nothing", list, err)
	}
}

// A store that cannot be trusted says so: metadata that does not parse, or that names
// another digest than the directory it sits in, is an error rather than a release.
func TestListRefusesMetadataItCannotTrust(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, want string
	}{
		{"not JSON", "{", "unexpected end"},
		{"another digest", `{"id":"com.example.echo","version":"1.0.0","digest":"sha256:ffff"}`, "records digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			release := dir + "/com.example.echo/1.0.0/abcd"
			if err := os.MkdirAll(release, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(release+"/"+metadataFile, []byte(tc.metadata), 0o600); err != nil {
				t.Fatal(err)
			}
			// A stray file beside the ids, and one beside the versions, are skipped.
			_ = os.WriteFile(dir+"/stray", nil, 0o600)
			_ = os.WriteFile(dir+"/com.example.echo/stray", nil, 0o600)
			if _, err := s.List(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("List = %v, want an error naming %q", err, tc.want)
			}
			if _, err := s.Verified("com.example.echo", "1.0.0"); err == nil {
				t.Error("Verified = nil error on untrustworthy metadata")
			}
		})
	}
}

// An artifact that cannot be read is not installed, and a store cannot be opened
// where a file stands.
func TestInstallAndOpenFailures(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin := []byte("payload")
	m := manifestFor("1.0.0", Platform(), digestOf(bin), workertype.MediaTypeExecutable)
	if _, err := s.Install(m, failingReader{}, Platform(), now); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Errorf("Install from a failing reader = %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("a failed install left %+v", list)
	}
	file := dir + "/not-a-dir"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file + "/store"); err == nil {
		t.Error("Open under a file = nil error")
	}
	// A path outside the store is never read as a release.
	if _, err := s.readVersionLocked(dir + "/../elsewhere"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("readVersionLocked outside the store = %v, want not found", err)
	}
	if executableName() == "" || Platform() == "" {
		t.Error("executableName or Platform is empty")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// Several versions of one Worker Type list in order, and a manifest whose runtime is
// not external is not an artifact to install.
func TestVersionsListInOrderAndOnlyExternalRuntimesInstall(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"1.1.0", "1.0.0"} {
		bin := []byte("payload " + v)
		if _, err := s.Install(manifestFor(v, Platform(), digestOf(bin), workertype.MediaTypeExecutable), bytes.NewReader(bin), Platform(), now); err != nil {
			t.Fatalf("Install %s: %v", v, err)
		}
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].Version != "1.0.0" || list[1].Version != "1.1.0" {
		t.Fatalf("List = %+v, %v; want both versions in order", list, err)
	}
	// Removing one version keeps the other and its id directory.
	if err := s.Remove("com.example.echo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 1 || list[0].Version != "1.1.0" {
		t.Errorf("List after removing 1.0.0 = %+v", list)
	}
	bundled := bytes.Replace(manifestFor("2.0.0", Platform(), digestOf([]byte("x")), workertype.MediaTypeExecutable),
		[]byte(`"mode":"external"`), []byte(`"mode":"bundled"`), 1)
	if _, err := s.Install(bundled, bytes.NewReader([]byte("x")), Platform(), now); err == nil || !strings.Contains(err.Error(), "only an external runtime") {
		t.Errorf("Install of a bundled runtime = %v, want a refusal", err)
	}
}

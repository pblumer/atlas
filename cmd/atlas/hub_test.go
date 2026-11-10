package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/api/blueprintregistry"
)

const (
	hubExamples    = "../../examples"
	hubCatalogFile = "../../api/web/examples-catalog.json"
	hubReleases    = "../../examples/hub/releases.json"
	hubBundles     = "../../examples/hub/bundles.json"
)

func hubTestKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("atlas hub build deterministic test-only key"))
	return ed25519.NewKeyFromSeed(seed[:])
}

// TestHubReleasesAreCurrent is the guard that keeps a version immutable: every
// sample's package must still be the digest its latest recorded version names.
// Changing an example without recording a new version fails here, before a
// publication could put other content under a version an installation pinned.
func TestHubReleasesAreCurrent(t *testing.T) {
	lock, err := readHubReleases(hubReleases)
	if err != nil {
		t.Fatal(err)
	}
	samples, err := loadHubSamples(hubExamples, hubCatalogFile, hubBundles)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 14 {
		t.Fatalf("%d bundles composed, want the 14 of bundles.json", len(samples))
	}
	if _, _, err := hubIndex(lock, samples); err != nil {
		t.Fatalf("%v\n\nAfter changing an example, regenerate the catalogue and record its new version:\n"+
			"  go test ./examples -run TestExampleCatalogMatchesTheFiles -update\n  go run ./cmd/atlas hub lock", err)
	}
	// A server of this very build must be offered every sample, or the hub
	// would list nothing to the installations it is built alongside.
	for _, s := range samples {
		latest, _ := lock.latest(s.ID)
		if !blueprintregistry.Compatible(blueprintregistry.Compatibility{MinVersion: latest.MinAtlas}, api.Version) {
			t.Errorf("%s@%s requires atlas %s, which this build (%s) is not", s.ID, latest.Version, latest.MinAtlas, api.Version)
		}
	}
}

// TestHubBuildWritesARegistryAServerAccepts: what build writes verifies with the
// public key alone, and every listed package passes the server's inspection.
func TestHubBuildWritesARegistryAServerAccepts(t *testing.T) {
	key := hubTestKey()
	public := key.Public().(ed25519.PublicKey)
	releases := hubReleasesWithKey(t, blueprintregistry.EncodePublicKey(public))
	keyFile := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(keyFile, []byte(blueprintregistry.EncodeSigningKey(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "hub")
	var out bytes.Buffer
	args := []string{"build", "--examples", hubExamples, "--catalog", hubCatalogFile, "--bundles", hubBundles, "--releases", releases, "--key-file", keyFile, dir}
	if err := runHub(args, &out); err != nil {
		t.Fatalf("build: %v\n%s", err, out.String())
	}
	index := readBuiltIndex(t, dir, public)
	if len(index.Releases) != 14 || index.RegistryID != "io.github.pblumer.atlas.hub" {
		t.Fatalf("index = %s with %d releases", index.RegistryID, len(index.Releases))
	}
	for _, r := range index.Releases {
		archive, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(r.Artifact.Path)))
		if err != nil {
			t.Fatal(err)
		}
		inspected, err := blueprintregistry.InspectPackage(r, archive)
		if err != nil {
			t.Errorf("%s: %v", r.BlueprintID, err)
			continue
		}
		if inspected.ApplicationKey == "" {
			t.Errorf("%s carries no application key", r.BlueprintID)
		}
		if len(r.Tags) == 0 {
			t.Errorf("%s carries no tags", r.BlueprintID)
		}
	}

	// A second build into the same directory is refused rather than mixed in.
	if err := runHub(args, &out); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a build over an earlier one = %v", err)
	}
	// Another key than the one releases.json records signs nothing.
	other := filepath.Join(t.TempDir(), "other.key")
	if err := os.WriteFile(other, []byte(blueprintregistry.EncodeSigningKey(ed25519.NewKeyFromSeed(make([]byte, 32)))), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runHub([]string{"build", "--examples", hubExamples, "--catalog", hubCatalogFile, "--bundles", hubBundles, "--releases", releases, "--key-file", other, filepath.Join(t.TempDir(), "x")}, &out)
	if err == nil || !strings.Contains(err.Error(), "is not the publicKey") {
		t.Fatalf("a build with another key = %v", err)
	}
	// Without a recorded public key operators would have nothing to pin.
	err = runHub([]string{"build", "--examples", hubExamples, "--catalog", hubCatalogFile, "--bundles", hubBundles, "--releases", hubReleasesWithKey(t, ""), "--key-file", keyFile, filepath.Join(t.TempDir(), "y")}, &out)
	if err == nil || !strings.Contains(err.Error(), "records no publicKey") {
		t.Fatalf("a build without a recorded key = %v", err)
	}
}

// TestHubLockRecordsANewVersionForChangedContent: a sample whose files changed
// gets the next patch version, its earlier version stays recorded with its own
// digest, and an unchanged sample gets nothing.
func TestHubLockRecordsANewVersionForChangedContent(t *testing.T) {
	lock, err := readHubReleases(hubReleases)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend einstieg@1.0.0 was published with other bytes.
	for i := range lock.Releases {
		if lock.Releases[i].Bundle == "einstieg" {
			lock.Releases[i].Digest = "sha256:" + strings.Repeat("0", 64)
		}
	}
	file := filepath.Join(t.TempDir(), "releases.json")
	if err := writeHubReleases(file, lock); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHub([]string{"lock", "--examples", hubExamples, "--catalog", hubCatalogFile, "--bundles", hubBundles, "--releases", file}, &out); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if !strings.Contains(out.String(), "einstieg: ") || !strings.Contains(out.String(), "1 new versions recorded") {
		t.Fatalf("lock output:\n%s", out.String())
	}
	after, err := readHubReleases(file)
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := after.latest("einstieg")
	if latest.Version == "1.0.0" || latest.Digest == "sha256:"+strings.Repeat("0", 64) {
		t.Fatalf("latest = %+v", latest)
	}
	if len(after.Releases) != len(lock.Releases)+1 {
		t.Fatalf("lock has %d entries, want the %d it had and one more", len(after.Releases), len(lock.Releases))
	}
}

// TestEverySampleInstallsFromItsHubPackage: the package the hub lists for each
// sample is one the ordinary import installs and publishes — the same routes the
// Console takes after its preview — and installing it again changes nothing.
// A sample with a shop asks its questions; the test answers them as an operator
// would, with groups and a person of this installation.
func TestEverySampleInstallsFromItsHubPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("installs every sample")
	}
	samples, err := loadHubSamples(hubExamples, hubCatalogFile, hubBundles)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := liveServer(t)
	group := createdID(t, ts, "/api/v1/groups", `{"name":"Hub Zielgruppe"}`)
	createdID(t, ts, "/api/v1/users", `{"username":"hubapprover","displayName":"Hub Approver","password":"a-long-password-1"}`)
	for _, s := range samples {
		t.Run(s.ID, func(t *testing.T) {
			archive, err := blueprintregistry.WritePackage(s.Files)
			if err != nil {
				t.Fatal(err)
			}
			// The Console's first step after the preview: the whole signed archive,
			// shop documents and all, goes to the source route as a dry run.
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/applications/source?dryRun=true", bytes.NewReader(archive))
			req.Header.Set("Content-Type", "application/gzip")
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the Console's dry run of the hub archive = %d %s", resp.StatusCode, body)
			}
			dir := unpackHubPackage(t, archive)
			args := []string{"--server", ts.URL, "--note", "atlas hub"}
			// A package that brings its architecture names capabilities this empty map
			// lacks: the person installing it decides each, here by adopting what the
			// package proposes (ADR-0528).
			var plan struct {
				Artifacts []struct {
					Ref      string `json:"ref"`
					Status   string `json:"status"`
					Proposed bool   `json:"proposed"`
				} `json:"artifacts"`
			}
			if err := json.Unmarshal(body, &plan); err != nil {
				t.Fatal(err)
			}
			var adopt []string
			for _, a := range plan.Artifacts {
				if a.Status == "gap" && a.Proposed {
					adopt = append(adopt, a.Ref)
				}
			}
			if len(adopt) > 0 {
				args = append(args, "--adopt", strings.Join(adopt, ","))
			}
			if questions, err := readOptional(filepath.Join(dir, packageQuestions)); err != nil {
				t.Fatal(err)
			} else if questions != nil {
				args = append(args, "--answers", hubAnswers(t, questions, group))
			}
			args = append(args, dir)
			var out bytes.Buffer
			if err := runImport(args, &out); err != nil {
				t.Fatalf("install: %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "published release") && !strings.Contains(out.String(), "is unchanged") {
				t.Fatalf("the application was not published:\n%s", out.String())
			}
			out.Reset()
			if err := runImport(args, &out); err != nil {
				t.Fatalf("second install: %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "not published again") {
				t.Errorf("a second install of an unchanged package changed something:\n%s", out.String())
			}
		})
	}
}

// hubAnswers answers every question of a package: a group question with the
// installation's group, a person with the test account.
func hubAnswers(t *testing.T, questions []byte, group string) string {
	t.Helper()
	var qs map[string]struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(questions, &qs); err != nil {
		t.Fatalf("fragen.json: %v", err)
	}
	answers := map[string]string{}
	for name, q := range qs {
		switch q.Kind {
		case "group":
			answers[name] = group
		default:
			answers[name] = "Hub Approver"
		}
	}
	return writeAnswers(t, answers)
}

func unpackHubPackage(t *testing.T, archive []byte) string {
	t.Helper()
	dir := t.TempDir()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hubReleasesWithKey(t *testing.T, publicKey string) string {
	t.Helper()
	lock, err := readHubReleases(hubReleases)
	if err != nil {
		t.Fatal(err)
	}
	lock.PublicKey = publicKey
	file := filepath.Join(t.TempDir(), "releases.json")
	if err := writeHubReleases(file, lock); err != nil {
		t.Fatal(err)
	}
	return file
}

func readBuiltIndex(t *testing.T, dir string, public ed25519.PublicKey) blueprintregistry.Index {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, "index.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := blueprintregistry.VerifySignedIndex(raw, sig, public)
	if err != nil {
		t.Fatalf("the built index does not verify: %v", err)
	}
	return index
}

// TestHubBundlesHoldEveryExampleOnce: a theme becomes one application, every
// installable example travels in exactly one bundle, and the shapes the import
// could not install as one application are refused before anything is built.
func TestHubBundlesHoldEveryExampleOnce(t *testing.T) {
	samples, err := loadHubSamples(hubExamples, hubCatalogFile, hubBundles)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, s := range samples {
		var man struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}
		for _, f := range s.Files {
			if f.Path == "atlas.json" {
				if err := json.Unmarshal(f.Data, &man); err != nil {
					t.Fatal(err)
				}
			}
		}
		if other, dup := keys[man.Key]; dup {
			t.Errorf("bundles %s and %s become the same application %s", other, s.ID, man.Key)
		}
		keys[man.Key] = s.ID
	}
	// The theme bundles take the bundle's name; a flagship keeps the handbook's.
	for id, want := range map[string]string{
		"einstieg": "beispiele-einstieg", "identitaet": "beispiele-identitaet-verzeichnis",
		"bewerbermanagement": "bewerbermanagement", "verwaltung-dienstleistungen": "beispiel-dienstleistungen-der-verwaltung",
	} {
		if keys[want] != id {
			t.Errorf("bundle %s is not the application %s (keys %v)", id, want, keys)
		}
	}

	for name, bundles := range map[string]string{
		"an example left out":       `{"bundles":[{"id":"x","tags":[],"examples":["pruefe-auftrag"]}]}`,
		"an example twice":          `{"bundles":[{"id":"x","name":"X","tags":[],"examples":["pruefe-auftrag","cart-total"]},{"id":"y","tags":[],"examples":["cart-total"]}]}`,
		"a study":                   `{"bundles":[{"id":"x","tags":[],"examples":["ad-objektmodell"]}]}`,
		"a shop with others":        `{"bundles":[{"id":"x","name":"X","tags":[],"examples":["m365-dienste","cart-total"]}]}`,
		"several without a name":    `{"bundles":[{"id":"x","tags":[],"examples":["pruefe-auftrag","cart-total"]}]}`,
		"a bad tag":                 `{"bundles":[{"id":"x","tags":["Grosses Tag"],"examples":["pruefe-auftrag"]}]}`,
		"an unknown field":          `{"bundles":[{"id":"x","tags":[],"category":"y","examples":["pruefe-auftrag"]}]}`,
		"an id that is not a label": `{"bundles":[{"id":"X.y","tags":[],"examples":["pruefe-auftrag"]}]}`,
	} {
		file := filepath.Join(t.TempDir(), "bundles.json")
		if err := os.WriteFile(file, []byte(bundles), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadHubSamples(hubExamples, hubCatalogFile, file); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestHubFingerprintChecksABackupAgainstTheRecordedKey: a restored backup or a
// rotated secret is compared with releases.json before it signs anything.
func TestHubFingerprintChecksABackupAgainstTheRecordedKey(t *testing.T) {
	key := hubTestKey()
	keyFile := filepath.Join(t.TempDir(), "hub.key")
	if err := os.WriteFile(keyFile, []byte(blueprintregistry.EncodeSigningKey(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	public := blueprintregistry.EncodePublicKey(key.Public().(ed25519.PublicKey))
	var out bytes.Buffer
	if err := runHub([]string{"fingerprint", "--key-file", keyFile, "--releases", hubReleasesWithKey(t, public)}, &out); err != nil {
		t.Fatalf("matching key: %v", err)
	}
	if !strings.Contains(out.String(), "matches") || strings.Contains(out.String(), blueprintregistry.EncodeSigningKey(key)) {
		t.Fatalf("output = %q (it must never print the private key)", out.String())
	}
	other := blueprintregistry.EncodePublicKey(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey))
	if err := runHub([]string{"fingerprint", "--key-file", keyFile, "--releases", hubReleasesWithKey(t, other)}, &out); err == nil {
		t.Fatal("a key that is not the recorded one passed")
	}
}

// TestHubAnnouncementIsVerifiedWithThePinnedKey: the current key announces its
// successor, an installation pinning the current key verifies the statement and
// is told what to pin, and one pinning another key — or a statement signed by
// somebody else — is refused.
func TestHubAnnouncementIsVerifiedWithThePinnedKey(t *testing.T) {
	current := hubTestKey()
	currentPublic := blueprintregistry.EncodePublicKey(current.Public().(ed25519.PublicKey))
	nextSeed := sha256.Sum256([]byte("atlas hub announce: next key"))
	next := ed25519.NewKeyFromSeed(nextSeed[:])
	nextPublic := blueprintregistry.EncodePublicKey(next.Public().(ed25519.PublicKey))
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "current.key")
	if err := os.WriteFile(keyFile, []byte(blueprintregistry.EncodeSigningKey(current)), 0o600); err != nil {
		t.Fatal(err)
	}
	statement := filepath.Join(dir, "key-rotation.json")
	releases := hubReleasesWithKey(t, currentPublic)
	var out bytes.Buffer
	if err := runHub([]string{"announce", "--key-file", keyFile, "--new-public-key", nextPublic, "--releases", releases, "--out", statement}, &out); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if strings.Contains(out.String(), blueprintregistry.EncodeSigningKey(current)) {
		t.Fatal("announce printed the private key")
	}

	out.Reset()
	if err := runHub([]string{"verify-announcement", "--pinned", currentPublic, "--statement", statement}, &out); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(out.String(), "ATLAS_BLUEPRINT_REGISTRY_KEY="+currentPublic+","+nextPublic) {
		t.Fatalf("verify does not say what to pin:\n%s", out.String())
	}

	stranger := blueprintregistry.EncodePublicKey(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey))
	if err := runHub([]string{"verify-announcement", "--pinned", stranger, "--statement", statement}, &out); err == nil ||
		!strings.Contains(err.Error(), "NOT a valid announcement") {
		t.Fatalf("a statement verified against a key that did not sign it: %v", err)
	}
	// A forger rewrites the statement to name their own key: the signature no
	// longer matches.
	raw, err := os.ReadFile(statement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statement, []byte(strings.Replace(string(raw), nextPublic, stranger, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runHub([]string{"verify-announcement", "--pinned", currentPublic, "--statement", statement}, &out); err == nil {
		t.Fatal("a rewritten statement verified")
	}

	// Only the key releases.json records may announce.
	if err := runHub([]string{"announce", "--key-file", keyFile, "--new-public-key", nextPublic, "--releases", hubReleasesWithKey(t, nextPublic), "--out", statement}, &out); err == nil {
		t.Fatal("a key that is not the recorded one announced a rotation")
	}
	if err := runHub([]string{"announce", "--key-file", keyFile, "--new-public-key", nextPublic, "--releases", releases, "--days", "3", "--out", statement}, &out); err == nil {
		t.Fatal("a rotation with three days' notice was announced")
	}
}

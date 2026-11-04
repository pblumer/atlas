package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/api/apppkg"
	"github.com/pblumer/atlas/api/blueprintregistry"
)

// `atlas hub` is the publisher's side of atlas hub (ADR-0499): it turns the
// examples this repository ships into the signed static registry a server reads
// through ATLAS_BLUEPRINT_REGISTRY_URL and ATLAS_BLUEPRINT_REGISTRY_KEY, so the
// samples reach an installation the way any other Blueprint does — listed,
// previewed, verified, and imported through the ordinary package import.
//
//	atlas hub keygen --out FILE        a new registry signing key; prints its public half
//	atlas hub fingerprint --key-file F which public key a key file (a backup) belongs to
//	atlas hub announce --key-file F --new-public-key K
//	                                   a key rotation statement, signed with the current key
//	atlas hub verify-announcement      check a rotation statement against the pinned key(s)
//	atlas hub lock                     record a new version for every bundle that changed
//	atlas hub build --key-file F DIR   write index.json, index.json.sig and packages/ to DIR
//
// examples/hub/bundles.json says which examples travel together: small examples
// by theme, one application per theme, so trying them out leaves a handful of
// applications rather than one per model; the handbook's flagships and the shop
// packages keep their own.
//
// Versions are immutable. A bundle's package is a deterministic function of its
// files, and examples/hub/releases.json records every (version, digest) it was
// ever given: `lock` adds a version when the digest moved, `build` refuses to
// publish a bundle whose files no longer match its latest recorded version. A
// version therefore never names two contents, which is what lets an installation
// pin one (BMP2).
//
// Nothing here runs in a server. The private key lives with whoever publishes —
// a CI secret and an offline backup — and a server only ever holds the public key.

// hubReleasesFile is the release lock: the registry's identity and every version
// each sample was published under.
type hubReleasesFile struct {
	RegistryID  string `json:"registryId"`
	PublisherID string `json:"publisherId"`
	License     string `json:"license"`
	// PublicKey is the base64 Ed25519 public key the index is signed for, as
	// operators set ATLAS_BLUEPRINT_REGISTRY_KEY. build refuses any other key, so
	// a mistaken secret cannot publish an index no installation accepts.
	PublicKey string           `json:"publicKey"`
	Releases  []hubLockedEntry `json:"releases"`
}

type hubLockedEntry struct {
	Bundle  string `json:"bundle"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	// MinAtlas is the oldest atlas the version is offered to, fixed when the
	// version is recorded: a later atlas does not withdraw it from older servers.
	MinAtlas string `json:"minAtlas"`
}

// hubSample is one example as a package.
type hubSample struct {
	ID              string
	Title           string
	Description     string
	Files           []blueprintregistry.SourceFile
	WorkerTypes     []string
	ServerConstants []string
	Tags            []string
}

// hubManifest mirrors the server's atlas.json (api/appsource.go, sourceManifest)
// for a package composed from loose models.
type hubManifest struct {
	FormatVersion int                  `json:"formatVersion"`
	Key           string               `json:"key"`
	Name          string               `json:"name"`
	Processes     []hubManifestEntry   `json:"processes"`
	Forms         []hubManifestEntry   `json:"forms"`
	Decisions     []hubManifestDecided `json:"decisions"`
}

type hubManifestEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type hubManifestDecided struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ModelRef string `json:"modelRef"`
	Path     string `json:"path,omitempty"`
}

func runHub(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("name a step: atlas hub keygen|fingerprint|announce|verify-announcement|lock|build [flags]")
	}
	switch args[0] {
	case "keygen":
		return runHubKeygen(args[1:], out)
	case "fingerprint":
		return runHubFingerprint(args[1:], out)
	case "announce":
		return runHubAnnounce(args[1:], out)
	case "verify-announcement":
		return runHubVerifyAnnouncement(args[1:], out)
	case "lock":
		return runHubLock(args[1:], out)
	case "build":
		return runHubBuild(args[1:], out)
	default:
		return fmt.Errorf("unknown step %q: atlas hub keygen|fingerprint|announce|verify-announcement|lock|build [flags]", args[0])
	}
}

func runHubKeygen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub keygen", flag.ContinueOnError)
	file := fs.String("out", "", "the file the private signing key is written to (mode 0600); it must not exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return errors.New("name the key file: atlas hub keygen --out FILE")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// O_EXCL: a key is never overwritten, because the registry it signed would
	// then be signed by nobody who still holds the key.
	f, err := os.OpenFile(*file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write the signing key: %w", err)
	}
	if _, err := fmt.Fprintln(f, blueprintregistry.EncodeSigningKey(private)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(out, "signing key written to %s — keep it secret; it is what makes the registry's word worth anything\n", *file)
	fmt.Fprintf(out, "public key (releases.json \"publicKey\", ATLAS_BLUEPRINT_REGISTRY_KEY): %s\n", blueprintregistry.EncodePublicKey(public))
	fmt.Fprintf(out, "fingerprint: %s\n", blueprintregistry.KeyFingerprint(public))
	return nil
}

// runHubFingerprint says which public key a signing key file belongs to, and
// whether that is the key releases.json records — the check a restored backup
// or a rotated secret has to pass before anything is signed with it. It prints
// the public half only.
func runHubFingerprint(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub fingerprint", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "the private signing key (or ATLAS_HUB_SIGNING_KEY)")
	releases := fs.String("releases", filepath.Join("examples", "hub", "releases.json"), "the release lock whose publicKey the key is compared with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("atlas hub fingerprint takes no arguments")
	}
	key, err := hubSigningKey(*keyFile)
	if err != nil {
		return err
	}
	public := key.Public().(ed25519.PublicKey)
	fmt.Fprintf(out, "public key: %s\nfingerprint: %s\n", blueprintregistry.EncodePublicKey(public), blueprintregistry.KeyFingerprint(public))
	lock, err := readHubReleases(*releases)
	if err != nil {
		return err
	}
	switch lock.PublicKey {
	case "":
		return fmt.Errorf("%s records no publicKey yet", *releases)
	case blueprintregistry.EncodePublicKey(public):
		fmt.Fprintf(out, "matches the publicKey %s records\n", *releases)
		return nil
	default:
		return fmt.Errorf("does NOT match the publicKey %s records (%s)", *releases, lock.PublicKey)
	}
}

// The fixed place a key rotation statement is published at, in the repository:
// behind the same review as releases.json, and copied to the Pages site beside
// the index. Operators accept a statement from here, not one that was mailed.
var (
	hubRotationFile = filepath.Join("examples", "hub", "key-rotation.json")
	hubRotationDays = 14
)

// runHubAnnounce writes the statement that announces the registry's next
// signing key, signed with the current one (ADR-0499 amendment 2026-11-02 c).
// The current key must be the one releases.json records, so the statement is
// verifiable by every installation that pins it.
func runHubAnnounce(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub announce", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "the CURRENT private signing key (or ATLAS_HUB_SIGNING_KEY)")
	newKey := fs.String("new-public-key", "", "the successor's public key, as atlas hub keygen printed it")
	days := fs.Int("days", hubRotationDays, "days between today and the earliest switch to the new key")
	releases := fs.String("releases", filepath.Join("examples", "hub", "releases.json"), "the release lock whose publicKey is the current key")
	outFile := fs.String("out", hubRotationFile, "where the statement is written; its signature goes beside it with .sig")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("atlas hub announce takes no arguments")
	}
	current, err := hubSigningKey(*keyFile)
	if err != nil {
		return err
	}
	lock, err := readHubReleases(*releases)
	if err != nil {
		return err
	}
	currentPublic := current.Public().(ed25519.PublicKey)
	if lock.PublicKey != blueprintregistry.EncodePublicKey(currentPublic) {
		return fmt.Errorf("the signing key is not the publicKey %s records; a rotation is announced with the current key", *releases)
	}
	next, err := parsePublicKey(*newKey)
	if err != nil {
		return fmt.Errorf("--new-public-key: %w", err)
	}
	now := time.Now().UTC()
	stmt := blueprintregistry.NewKeyRotation(lock.RegistryID, currentPublic, next, now, now.AddDate(0, 0, *days))
	raw, sig, err := blueprintregistry.SignKeyRotation(stmt, current)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outFile, raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(*outFile+".sig", sig, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "rotation from %s to %s announced, switch not before %s\nwrote %s and %s.sig — commit both to main; operators verify them with atlas hub verify-announcement\n",
		stmt.OldFingerprint, stmt.NewFingerprint, stmt.SwitchNotBefore, *outFile, *outFile)
	return nil
}

// runHubVerifyAnnouncement is the operator's check: is this statement signed by
// a key this installation already pins, and which key does it announce? It
// prints the variable to set for the overlap, never anything secret.
func runHubVerifyAnnouncement(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub verify-announcement", flag.ContinueOnError)
	pinned := fs.String("pinned", os.Getenv("ATLAS_BLUEPRINT_REGISTRY_KEY"), "the key(s) this installation pins, as in ATLAS_BLUEPRINT_REGISTRY_KEY (the default)")
	statement := fs.String("statement", hubRotationFile, "the rotation statement; its signature is the same path with .sig")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("atlas hub verify-announcement takes no arguments")
	}
	if strings.TrimSpace(*pinned) == "" {
		return errors.New("name the pinned key(s): --pinned, or ATLAS_BLUEPRINT_REGISTRY_KEY")
	}
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(*pinned, ",") {
		k, err := parsePublicKey(part)
		if err != nil {
			return fmt.Errorf("--pinned: %w", err)
		}
		keys = append(keys, k)
	}
	raw, err := os.ReadFile(*statement)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*statement + ".sig")
	if err != nil {
		return err
	}
	r, err := blueprintregistry.VerifyKeyRotation(raw, []byte(strings.TrimSpace(string(sig))), keys)
	if err != nil {
		return fmt.Errorf("NOT a valid announcement — do not pin its key: %w", err)
	}
	old := ""
	for _, k := range keys {
		if blueprintregistry.KeyFingerprint(k) == r.OldFingerprint {
			old = blueprintregistry.EncodePublicKey(k)
		}
	}
	fmt.Fprintf(out, "valid announcement for registry %s, signed by the pinned key %s\n", r.RegistryID, r.OldFingerprint)
	fmt.Fprintf(out, "new key: %s\nnew fingerprint: %s\nannounced %s, switch not before %s\n", r.NewPublicKey, r.NewFingerprint, r.Announced, r.SwitchNotBefore)
	fmt.Fprintf(out, "pin both until the switch is done:\n  ATLAS_BLUEPRINT_REGISTRY_KEY=%s,%s\n", old, r.NewPublicKey)
	return nil
}

// parsePublicKey reads one base64 32-byte Ed25519 public key.
func parsePublicKey(text string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64 32-byte Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

// hubSourceFlags are the inputs lock and build share.
type hubSourceFlags struct {
	examples, catalog, bundles, releases *string
}

func addHubSourceFlags(fs *flag.FlagSet) hubSourceFlags {
	return hubSourceFlags{
		examples: fs.String("examples", "examples", "the examples directory"),
		catalog:  fs.String("catalog", filepath.Join("api", "web", "examples-catalog.json"), "the examples catalogue that says which files make up each example"),
		bundles:  fs.String("bundles", filepath.Join("examples", "hub", "bundles.json"), "which examples travel together as one application"),
		releases: fs.String("releases", filepath.Join("examples", "hub", "releases.json"), "the release lock"),
	}
}

func runHubLock(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub lock", flag.ContinueOnError)
	src := addHubSourceFlags(fs)
	minAtlas := fs.String("min-atlas", defaultMinAtlas(), "the oldest atlas a newly recorded version is offered to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("atlas hub lock takes no arguments")
	}
	if !blueprintregistry.ValidVersion(*minAtlas) {
		return fmt.Errorf("--min-atlas %q is not a semantic version", *minAtlas)
	}
	lock, err := readHubReleases(*src.releases)
	if err != nil {
		return err
	}
	samples, err := loadHubSamples(*src.examples, *src.catalog, *src.bundles)
	if err != nil {
		return err
	}
	changed := 0
	for _, s := range samples {
		archive, err := blueprintregistry.WritePackage(s.Files)
		if err != nil {
			return fmt.Errorf("sample %s: %w", s.ID, err)
		}
		digest := blueprintregistry.ArtifactOf("x.tar.gz", archive).Digest
		latest, ok := lock.latest(s.ID)
		if ok && latest.Digest == digest {
			continue
		}
		version := "1.0.0"
		if ok {
			if version, err = blueprintregistry.NextPatch(latest.Version); err != nil {
				return fmt.Errorf("sample %s: %w", s.ID, err)
			}
		}
		lock.Releases = append(lock.Releases, hubLockedEntry{Bundle: s.ID, Version: version, Digest: digest, MinAtlas: *minAtlas})
		fmt.Fprintf(out, "%s: %s\n", s.ID, version)
		changed++
	}
	if changed == 0 {
		fmt.Fprintf(out, "%d bundles, none changed\n", len(samples))
		return nil
	}
	if err := writeHubReleases(*src.releases, lock); err != nil {
		return err
	}
	fmt.Fprintf(out, "%d bundles, %d new versions recorded in %s\n", len(samples), changed, *src.releases)
	return nil
}

func runHubBuild(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("hub build", flag.ContinueOnError)
	src := addHubSourceFlags(fs)
	keyFile := fs.String("key-file", "", "the private signing key, as atlas hub keygen wrote it (or ATLAS_HUB_SIGNING_KEY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("name the output directory: atlas hub build --key-file FILE DIR")
	}
	dir := fs.Arg(0)
	key, err := hubSigningKey(*keyFile)
	if err != nil {
		return err
	}
	lock, err := readHubReleases(*src.releases)
	if err != nil {
		return err
	}
	if lock.PublicKey == "" {
		return fmt.Errorf("%s records no publicKey: put the public key atlas hub keygen printed there first, so operators can pin it", *src.releases)
	}
	if got := blueprintregistry.EncodePublicKey(key.Public().(ed25519.PublicKey)); got != lock.PublicKey {
		return fmt.Errorf("the signing key's public half %s is not the publicKey %s records; installations pinned to that key would refuse this index", got, *src.releases)
	}
	samples, err := loadHubSamples(*src.examples, *src.catalog, *src.bundles)
	if err != nil {
		return err
	}
	index, archives, err := hubIndex(lock, samples)
	if err != nil {
		return err
	}
	raw, sig, err := blueprintregistry.SignIndex(index, key)
	if err != nil {
		return err
	}
	if err := emptyOutputDir(dir); err != nil {
		return err
	}
	for p, data := range archives {
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json.sig"), sig, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "registry %s: %d bundles signed by %s\nwrote %s\n",
		index.RegistryID, len(index.Releases), blueprintregistry.KeyFingerprint(key.Public().(ed25519.PublicKey)), dir)
	return nil
}

// hubIndex lists every sample at its latest recorded version, refusing a sample
// whose files no longer give that version's digest: it would otherwise publish
// other content under a version installations may already have pinned.
func hubIndex(lock hubReleasesFile, samples []hubSample) (blueprintregistry.Index, map[string][]byte, error) {
	index := blueprintregistry.Index{SchemaVersion: 1, RegistryID: lock.RegistryID, Releases: []blueprintregistry.Release{}}
	archives := map[string][]byte{}
	var stale []string
	for _, s := range samples {
		archive, err := blueprintregistry.WritePackage(s.Files)
		if err != nil {
			return index, nil, fmt.Errorf("sample %s: %w", s.ID, err)
		}
		latest, ok := lock.latest(s.ID)
		id := lock.PublisherID + "." + s.ID
		artifactPath := "packages/" + id + "-" + latest.Version + ".tar.gz"
		artifact := blueprintregistry.ArtifactOf(artifactPath, archive)
		if !ok || artifact.Digest != latest.Digest {
			stale = append(stale, s.ID)
			continue
		}
		archives[artifactPath] = archive
		index.Releases = append(index.Releases, blueprintregistry.Release{
			BlueprintID: id, Version: latest.Version, PublisherID: lock.PublisherID,
			Title: s.Title, Description: s.Description, License: lock.License,
			Atlas:    blueprintregistry.Compatibility{MinVersion: latest.MinAtlas},
			Artifact: artifact,
			Needs:    blueprintregistry.Needs{WorkerTypes: s.WorkerTypes, ServerConstants: s.ServerConstants},
			Tags:     s.Tags,
		})
	}
	if len(stale) > 0 {
		return index, nil, fmt.Errorf("bundles changed since their recorded version: %s — run `atlas hub lock` and commit releases.json",
			strings.Join(stale, ", "))
	}
	return index, archives, nil
}

// hubSigningKey reads the key from the file, else from ATLAS_HUB_SIGNING_KEY.
func hubSigningKey(file string) (ed25519.PrivateKey, error) {
	var text []byte
	switch {
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read the signing key: %w", err)
		}
		text = raw
	case os.Getenv("ATLAS_HUB_SIGNING_KEY") != "":
		text = []byte(os.Getenv("ATLAS_HUB_SIGNING_KEY"))
	default:
		return nil, errors.New("name the signing key: --key-file FILE, or ATLAS_HUB_SIGNING_KEY")
	}
	return blueprintregistry.ParseSigningKey(text)
}

// emptyOutputDir creates dir, or accepts it when it is empty: a build never
// mixes its files with a previous one's, which could leave a package listed by
// nobody, or worse, one listed under a stale digest.
func emptyOutputDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty", dir)
	}
	return nil
}

// defaultMinAtlas is the release line this binary belongs to, prereleases
// included: 0.21.0-dev gives 0.21.0-0, which a 0.21.0-dev server satisfies.
func defaultMinAtlas() string {
	v := api.Version
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return "0.1.0"
	}
	return parts[0] + "." + parts[1] + ".0-0"
}

func readHubReleases(file string) (hubReleasesFile, error) {
	var lock hubReleasesFile
	raw, err := os.ReadFile(file)
	if err != nil {
		return lock, fmt.Errorf("read the release lock: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return lock, fmt.Errorf("%s: %w", file, err)
	}
	if !blueprintregistry.ValidNamespace(lock.RegistryID) || !blueprintregistry.ValidNamespace(lock.PublisherID) {
		return lock, fmt.Errorf("%s: registryId and publisherId must be namespaced ids such as org.example.hub", file)
	}
	if strings.TrimSpace(lock.License) == "" {
		return lock, fmt.Errorf("%s names no licence", file)
	}
	seen := map[string]string{}
	for _, e := range lock.Releases {
		if !blueprintregistry.ValidVersion(e.Version) || !blueprintregistry.ValidVersion(e.MinAtlas) {
			return lock, fmt.Errorf("%s: %s has an invalid version or minAtlas", file, e.Bundle)
		}
		k := e.Bundle + "@" + e.Version
		if d, dup := seen[k]; dup && d != e.Digest {
			return lock, fmt.Errorf("%s: %s names two digests", file, k)
		} else if dup {
			return lock, fmt.Errorf("%s: %s is recorded twice", file, k)
		}
		seen[k] = e.Digest
	}
	if lock.Releases == nil {
		lock.Releases = []hubLockedEntry{}
	}
	return lock, nil
}

func writeHubReleases(file string, lock hubReleasesFile) error {
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(raw, '\n'), 0o644)
}

// latest is the last version recorded for a sample: the lock only ever appends.
func (l hubReleasesFile) latest(sample string) (hubLockedEntry, bool) {
	for i := len(l.Releases) - 1; i >= 0; i-- {
		if l.Releases[i].Bundle == sample {
			return l.Releases[i], true
		}
	}
	return hubLockedEntry{}, false
}

// hubBundlesFile is examples/hub/bundles.json: which examples travel together as
// one application. A bundle of several examples is filed into one application
// named after the bundle, so trying a theme leaves one application behind, not
// one per model; a bundle of one example keeps that example's own application —
// the handbook's flagships and the shop packages, whose key and catalogues are
// their own.
type hubBundlesFile struct {
	Bundles []hubBundleSpec `json:"bundles"`
}

type hubBundleSpec struct {
	ID string `json:"id"`
	// Name is the application a bundle of several examples becomes. A bundle of
	// one example takes that example's name and must not set one.
	Name     string   `json:"name,omitempty"`
	Tags     []string `json:"tags"`
	Examples []string `json:"examples"`
}

// hubExample is one catalogue entry, as the bundle composer reads it.
type hubExample struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	App       string `json:"app"`
	Processes []struct {
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		File      string `json:"file"`
	} `json:"processes"`
	Decisions []struct {
		Name   string `json:"name"`
		Handle string `json:"handle"`
		File   string `json:"file"`
	} `json:"decisions"`
	Forms []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		File string `json:"file"`
	} `json:"forms"`
	Shop *struct {
		App string `json:"app"`
	} `json:"shop"`
}

// installable says the handbook offers the example for installation: it has an
// application, or it is a shop package.
func (e hubExample) installable() bool { return e.App != "" || e.Shop != nil }

// loadHubSamples composes one package per bundle and checks the bundles against
// the catalogue: every installable example in exactly one bundle, nothing
// listed that the catalogue does not offer. A study or system-process example,
// which the handbook does not install, cannot be bundled.
func loadHubSamples(examplesDir, catalogFile, bundlesFile string) ([]hubSample, error) {
	raw, err := os.ReadFile(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read the examples catalogue: %w", err)
	}
	var cat struct {
		Examples []hubExample `json:"examples"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("%s: %w", catalogFile, err)
	}
	byID := map[string]hubExample{}
	for _, ex := range cat.Examples {
		byID[ex.ID] = ex
	}
	raw, err = os.ReadFile(bundlesFile)
	if err != nil {
		return nil, fmt.Errorf("read the bundles: %w", err)
	}
	var spec hubBundlesFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("%s: %w", bundlesFile, err)
	}
	claimed := map[string]string{}
	bundleIDs := map[string]bool{}
	var samples []hubSample
	for _, b := range spec.Bundles {
		if !hubBundleID.MatchString(b.ID) || bundleIDs[b.ID] {
			return nil, fmt.Errorf("%s: bundle id %q is invalid or given twice", bundlesFile, b.ID)
		}
		bundleIDs[b.ID] = true
		if len(b.Examples) == 0 {
			return nil, fmt.Errorf("bundle %s names no example", b.ID)
		}
		var members []hubExample
		for _, id := range b.Examples {
			ex, ok := byID[id]
			if !ok || !ex.installable() {
				return nil, fmt.Errorf("bundle %s names %q, which the catalogue does not offer for installation", b.ID, id)
			}
			if other, dup := claimed[id]; dup {
				return nil, fmt.Errorf("example %s is in bundle %s and in bundle %s", id, other, b.ID)
			}
			claimed[id] = b.ID
			members = append(members, ex)
		}
		s, err := composeBundle(examplesDir, b, members)
		if err != nil {
			return nil, fmt.Errorf("bundle %s: %w", b.ID, err)
		}
		samples = append(samples, s)
	}
	var unclaimed []string
	for _, ex := range cat.Examples {
		if ex.installable() && claimed[ex.ID] == "" {
			unclaimed = append(unclaimed, ex.ID)
		}
	}
	if len(unclaimed) > 0 {
		return nil, fmt.Errorf("%s does not bundle %s: give every installable example a bundle", bundlesFile, strings.Join(unclaimed, ", "))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ID < samples[j].ID })
	return samples, nil
}

// hubBundleID is the last label of a Blueprint id: lower case, inner hyphens.
var hubBundleID = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// composeBundle builds one bundle's package. A shop example travels as its own
// package directory and only alone; any other example is filed as the
// handbook's install button files it — the same process, form and decision
// identities — into the bundle's application.
func composeBundle(examplesDir string, b hubBundleSpec, members []hubExample) (hubSample, error) {
	var s hubSample
	single := len(members) == 1
	switch {
	case single && b.Name != "":
		return s, errors.New("a bundle of one example keeps that example's application; drop its name")
	case !single && strings.TrimSpace(b.Name) == "":
		return s, errors.New("a bundle of several examples needs the name of the application they become")
	}
	if members[0].Shop != nil {
		if !single {
			return s, fmt.Errorf("%s ships a shop, which travels only as its own package", members[0].ID)
		}
		var err error
		if s, err = shopSample(examplesDir, members[0].Path); err != nil {
			return s, err
		}
	} else {
		name := b.Name
		if single {
			name = members[0].App
		}
		s = hubSample{Title: name}
		man := hubManifest{
			FormatVersion: 1, Key: api.PortableApplicationKey(name), Name: name,
			Processes: []hubManifestEntry{}, Forms: []hubManifestEntry{}, Decisions: []hubManifestDecided{},
		}
		taken := map[string]bool{"atlas.json": true}
		ids := map[string]string{}
		add := func(dir, file string) (string, error) {
			p := dir + "/" + path.Base(file)
			if taken[p] {
				return "", fmt.Errorf("two files would be %s", p)
			}
			taken[p] = true
			data, err := os.ReadFile(filepath.Join(examplesDir, filepath.FromSlash(file)))
			if err != nil {
				return "", err
			}
			s.Files = append(s.Files, blueprintregistry.SourceFile{Path: p, Data: data})
			return p, nil
		}
		claim := func(kind, id, example string) error {
			if other, dup := ids[kind+":"+id]; dup {
				return fmt.Errorf("%s %s is in %s and in %s", kind, id, other, example)
			}
			ids[kind+":"+id] = example
			return nil
		}
		for _, ex := range members {
			if ex.Shop != nil {
				return s, fmt.Errorf("%s ships a shop, which travels only as its own package", ex.ID)
			}
			for _, pr := range ex.Processes {
				if err := claim("process", pr.ProcessID, ex.ID); err != nil {
					return s, err
				}
				p, err := add("processes", pr.File)
				if err != nil {
					return s, err
				}
				man.Processes = append(man.Processes, hubManifestEntry{ID: pr.ProcessID, Name: pr.Name, Path: p})
			}
			for _, d := range ex.Decisions {
				if err := claim("decision", d.Handle, ex.ID); err != nil {
					return s, err
				}
				p, err := add("decisions", d.File)
				if err != nil {
					return s, err
				}
				// The handle is the reference's id and its model's: one per model,
				// the shape the handbook installs.
				man.Decisions = append(man.Decisions, hubManifestDecided{ID: d.Handle, Name: d.Name, ModelRef: d.Handle, Path: p})
				man.FormatVersion = 2
			}
			for _, f := range ex.Forms {
				if err := claim("form", f.ID, ex.ID); err != nil {
					return s, err
				}
				p, err := add("forms", f.File)
				if err != nil {
					return s, err
				}
				man.Forms = append(man.Forms, hubManifestEntry{ID: f.ID, Name: f.Name, Path: p})
			}
		}
		data, err := json.MarshalIndent(man, "", "  ")
		if err != nil {
			return s, err
		}
		s.Files = append(s.Files, blueprintregistry.SourceFile{Path: "atlas.json", Data: append(data, '\n')})
	}
	s.ID = b.ID
	if err := describeSample(&s, members, b.Tags); err != nil {
		return s, err
	}
	return s, nil
}

// shopSample reads a package directory the way `atlas import` does: the manifest,
// the files it names, and the optional shop documents beside it. An answers file
// stays behind: answers belong to the installation that gives them.
func shopSample(examplesDir, dir string) (hubSample, error) {
	root := filepath.Join(examplesDir, filepath.FromSlash(dir))
	raw, err := os.ReadFile(filepath.Join(root, packageManifest))
	if err != nil {
		return hubSample{}, err
	}
	var man struct {
		Name      string `json:"name"`
		Constants string `json:"constants"`
		Processes []struct {
			Path string `json:"path"`
		} `json:"processes"`
		Forms []struct {
			Path string `json:"path"`
		} `json:"forms"`
		Decisions []struct {
			Path string `json:"path"`
		} `json:"decisions"`
		// The architecture a format-4 tree carries
		// (ADR-0528).
		InformationModels []struct {
			Path string `json:"path"`
		} `json:"informationModels"`
		Architecture string `json:"architecture"`
	}
	if err := json.Unmarshal(raw, &man); err != nil {
		return hubSample{}, fmt.Errorf("%s: %w", packageManifest, err)
	}
	paths := map[string]bool{}
	for _, m := range man.InformationModels {
		paths[m.Path] = true
	}
	if man.Architecture != "" {
		paths[man.Architecture] = true
	}
	for _, p := range man.Processes {
		paths[p.Path] = true
	}
	for _, f := range man.Forms {
		paths[f.Path] = true
	}
	for _, d := range man.Decisions {
		if d.Path != "" {
			paths[d.Path] = true
		}
	}
	if man.Constants != "" {
		paths[man.Constants] = true
	}
	s := hubSample{Title: man.Name, Files: []blueprintregistry.SourceFile{{Path: packageManifest, Data: raw}}}
	for p := range paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return hubSample{}, fmt.Errorf("%s names %q, which is outside the package", packageManifest, p)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return hubSample{}, err
		}
		s.Files = append(s.Files, blueprintregistry.SourceFile{Path: p, Data: data})
	}
	for _, name := range []string{packageCatalogue, packageQuestions, packageConstants} {
		data, err := readOptional(filepath.Join(root, name))
		if err != nil {
			return hubSample{}, err
		}
		if data != nil {
			s.Files = append(s.Files, blueprintregistry.SourceFile{Path: name, Data: data})
		}
	}
	return s, nil
}

// describeSample fills in what the index says about a bundle: the needs its
// processes name, its tags, and a description an operator can choose by.
func describeSample(s *hubSample, members []hubExample, bundleTags []string) error {
	processes := map[string][]byte{}
	forms, decisions, shop := 0, 0, false
	for _, f := range s.Files {
		switch {
		case strings.HasSuffix(f.Path, ".bpmn"):
			processes[f.Path] = f.Data
		case strings.HasSuffix(f.Path, ".dmn"):
			decisions++
		case f.Path == apppkg.CatalogueFile:
			shop = true
		case strings.HasPrefix(f.Path, "forms/") || strings.HasSuffix(f.Path, ".form.json"):
			forms++
		}
	}
	var unread []string
	s.WorkerTypes, s.ServerConstants, unread = api.ProcessNeeds(processes)
	if len(unread) > 0 {
		return fmt.Errorf("processes that do not compile are not published: %s", strings.Join(unread, ", "))
	}

	// The bundle's own tags, then what can be read off the package: each Worker
	// Type it names, and "shop" when it brings a catalogue.
	tags := map[string]bool{}
	for _, t := range bundleTags {
		if !blueprintregistry.ValidTag(t) {
			return fmt.Errorf("tag %q is not a lower-case label", t)
		}
		tags[t] = true
	}
	for _, w := range s.WorkerTypes {
		if blueprintregistry.ValidTag(w) {
			tags[w] = true
		}
	}
	if shop {
		tags["shop"] = true
	}
	s.Tags = make([]string, 0, len(tags))
	for t := range tags {
		s.Tags = append(s.Tags, t)
	}
	sort.Strings(s.Tags)
	if len(s.Tags) > blueprintregistry.MaxTags {
		return fmt.Errorf("%d tags, more than the index allows", len(s.Tags))
	}

	var desc string
	if len(members) == 1 {
		desc = "Beispiel aus examples/" + members[0].Path + ": "
	} else {
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, strings.TrimPrefix(m.App, "Beispiel: "))
		}
		desc = fmt.Sprintf("%d Beispiele in einer Applikation: %s. Zusammen ", len(members), strings.Join(names, ", "))
	}
	desc += fmt.Sprintf("%d %s, %d %s, %d %s",
		len(processes), plural2(len(processes), "Prozess", "Prozesse"),
		decisions, plural2(decisions, "Entscheidung", "Entscheidungen"),
		forms, plural2(forms, "Formular", "Formulare"))
	if shop {
		desc += ", mit Produktkatalog"
	}
	// Only what the models name a Worker Type for: a task bound to a job type no
	// Worker Type claims needs a worker this list cannot know about.
	if len(s.WorkerTypes) > 0 {
		desc += "; Worker-Typen: " + strings.Join(s.WorkerTypes, ", ")
	}
	s.Description = desc + "."
	if len(s.Title) > 120 || len(s.Description) > 1000 {
		return fmt.Errorf("title or description is longer than the index allows")
	}
	return nil
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

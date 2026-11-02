package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

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
//	atlas hub keygen --out FILE     a new registry signing key; prints its public half
//	atlas hub lock                  record a new version for every sample that changed
//	atlas hub build --key-file FILE DIR
//	                                write index.json, index.json.sig and packages/ to DIR
//
// Versions are immutable. A sample's package is a deterministic function of its
// files, and examples/hub/releases.json records every (version, digest) it was
// ever given: `lock` adds a version when the digest moved, `build` refuses to
// publish a sample whose files no longer match its latest recorded version. A
// version therefore never names two contents, which is what lets an installation
// pin one (BMP2).
//
// Nothing here runs in a server. The private key lives with whoever publishes —
// a CI secret — and a server only ever holds the public key.

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
	Sample  string `json:"sample"`
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
}

// hubCatalog is the part of api/web/examples-catalog.json a package is composed
// from: which files make up each example, and the identities the handbook's
// installer gives them. The content is read from the files themselves.
type hubCatalog struct {
	Examples []struct {
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
	} `json:"examples"`
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
		return errors.New("name a step: atlas hub keygen|lock|build [flags]")
	}
	switch args[0] {
	case "keygen":
		return runHubKeygen(args[1:], out)
	case "lock":
		return runHubLock(args[1:], out)
	case "build":
		return runHubBuild(args[1:], out)
	default:
		return fmt.Errorf("unknown step %q: atlas hub keygen|lock|build [flags]", args[0])
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

// hubSourceFlags are the inputs lock and build share.
type hubSourceFlags struct {
	examples, catalog, releases *string
}

func addHubSourceFlags(fs *flag.FlagSet) hubSourceFlags {
	return hubSourceFlags{
		examples: fs.String("examples", "examples", "the examples directory"),
		catalog:  fs.String("catalog", filepath.Join("api", "web", "examples-catalog.json"), "the examples catalogue that says which files make up each example"),
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
	samples, err := loadHubSamples(*src.examples, *src.catalog)
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
		lock.Releases = append(lock.Releases, hubLockedEntry{Sample: s.ID, Version: version, Digest: digest, MinAtlas: *minAtlas})
		fmt.Fprintf(out, "%s: %s\n", s.ID, version)
		changed++
	}
	if changed == 0 {
		fmt.Fprintf(out, "%d samples, none changed\n", len(samples))
		return nil
	}
	if err := writeHubReleases(*src.releases, lock); err != nil {
		return err
	}
	fmt.Fprintf(out, "%d samples, %d new versions recorded in %s\n", len(samples), changed, *src.releases)
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
	samples, err := loadHubSamples(*src.examples, *src.catalog)
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
	fmt.Fprintf(out, "registry %s: %d samples signed by %s\nwrote %s\n",
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
		})
	}
	if len(stale) > 0 {
		return index, nil, fmt.Errorf("samples changed since their recorded version: %s — run `atlas hub lock` and commit releases.json",
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
			return lock, fmt.Errorf("%s: %s has an invalid version or minAtlas", file, e.Sample)
		}
		k := e.Sample + "@" + e.Version
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
		if l.Releases[i].Sample == sample {
			return l.Releases[i], true
		}
	}
	return hubLockedEntry{}, false
}

// loadHubSamples composes one package per installable example. An example that
// ships a shop is already a package directory (atlas.json, katalog.json,
// fragen.json) and travels as `atlas import` reads it; any other example is
// filed into an application exactly as the handbook's install button files it —
// same application name, so the same portable key, the same process, form and
// decision identities. A study or system-process example has no application and
// is not offered.
func loadHubSamples(examplesDir, catalogFile string) ([]hubSample, error) {
	raw, err := os.ReadFile(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read the examples catalogue: %w", err)
	}
	var cat hubCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("%s: %w", catalogFile, err)
	}
	var samples []hubSample
	for _, ex := range cat.Examples {
		var s hubSample
		switch {
		case ex.Shop != nil:
			if s, err = shopSample(examplesDir, ex.Path); err != nil {
				return nil, fmt.Errorf("example %s: %w", ex.ID, err)
			}
		case ex.App != "":
			s = hubSample{Title: ex.App}
			man := hubManifest{
				FormatVersion: 1, Key: api.PortableApplicationKey(ex.App), Name: ex.App,
				Processes: []hubManifestEntry{}, Forms: []hubManifestEntry{}, Decisions: []hubManifestDecided{},
			}
			taken := map[string]bool{"atlas.json": true}
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
			for _, pr := range ex.Processes {
				p, err := add("processes", pr.File)
				if err != nil {
					return nil, fmt.Errorf("example %s: %w", ex.ID, err)
				}
				man.Processes = append(man.Processes, hubManifestEntry{ID: pr.ProcessID, Name: pr.Name, Path: p})
			}
			for _, d := range ex.Decisions {
				p, err := add("decisions", d.File)
				if err != nil {
					return nil, fmt.Errorf("example %s: %w", ex.ID, err)
				}
				// The handle is the reference's id and its model's: one per model,
				// the shape the handbook installs.
				man.Decisions = append(man.Decisions, hubManifestDecided{ID: d.Handle, Name: d.Name, ModelRef: d.Handle, Path: p})
				man.FormatVersion = 2
			}
			for _, f := range ex.Forms {
				p, err := add("forms", f.File)
				if err != nil {
					return nil, fmt.Errorf("example %s: %w", ex.ID, err)
				}
				man.Forms = append(man.Forms, hubManifestEntry{ID: f.ID, Name: f.Name, Path: p})
			}
			data, err := json.MarshalIndent(man, "", "  ")
			if err != nil {
				return nil, err
			}
			s.Files = append(s.Files, blueprintregistry.SourceFile{Path: "atlas.json", Data: append(data, '\n')})
		default:
			continue
		}
		s.ID = ex.ID
		if err := describeSample(&s, ex.Path); err != nil {
			return nil, fmt.Errorf("example %s: %w", ex.ID, err)
		}
		samples = append(samples, s)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ID < samples[j].ID })
	return samples, nil
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
	}
	if err := json.Unmarshal(raw, &man); err != nil {
		return hubSample{}, fmt.Errorf("%s: %w", packageManifest, err)
	}
	paths := map[string]bool{}
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

// describeSample fills in what the index says about a sample: the needs its
// processes name, and a description an operator can choose by.
func describeSample(s *hubSample, where string) error {
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
	desc := fmt.Sprintf("Beispiel aus examples/%s: %d %s, %d %s, %d %s", where,
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
	if len(s.Title) > 120 {
		return fmt.Errorf("title %q is longer than the index allows", s.Title)
	}
	return nil
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

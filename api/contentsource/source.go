// Package contentsource reads a service's content — its description, its
// keywords, its picture — from a system outside Atlas, and decides whether any
// of it may reach the shop (ADR-0491).
//
// Nothing here is engine state. A source is design-time configuration, a fetch is
// a catalogue-service request made before any command and outside the processor,
// and what it yields is ordinary catalogue data written through the run loop. No
// record is replayed, so nothing here is fetched during replay, and I2, I4 and I6
// are untouched (§ 7 of the record).
//
// The package does not know the catalogue. It answers three questions for it —
// what a page says, whether a page may be shown, and whether the account Atlas
// reads with is what it must be — and the catalogue decides what to do with the
// answers. That keeps the security rules a pure function of the facts an adapter
// returns (rules.go), which is what lets them be tested row by row.
package contentsource

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// KindWikiWikiBangBang is the first source (§ 3): the typed item model whose
// pages a product owner keeps one per language.
const KindWikiWikiBangBang = "wikiwikibangbang"

// Kinds are the source kinds this build has an adapter for. A kind is admitted
// only once its adapter proves the facts of § 10, so the list is closed and a
// configuration naming anything else is refused rather than stored for a later
// build to interpret.
var Kinds = []string{KindWikiWikiBangBang}

// Source is one configured content source (§ 3).
//
// It is configuration an administrator writes and nothing else does. The
// credential is a reference into the secret store (ADR-0041), never the token
// itself: a source record is what a backup carries and what an API answer shows,
// and a token in either is a token leaving the building.
type Source struct {
	// ID names the source in a binding. It is chosen, not minted, because a
	// binding names it for as long as the item exists.
	ID string `json:"id"`
	// Name is what a product manager reads when choosing a source.
	Name string `json:"name"`
	// Kind is the adapter, one of [Kinds].
	Kind string `json:"kind"`
	// BaseURL is where the source answers, https only. A page address of any
	// other host is refused when it is pasted (§ 5), and the adapter follows no
	// redirect away from it.
	BaseURL string `json:"baseURL"`
	// CredentialsRef names the vault secret (or ATLAS_CONNECTOR_<REF>_TOKEN)
	// holding the technical account's read token.
	CredentialsRef string `json:"credentialsRef"`
	// PictureField is the property of the primary page that names the
	// attachments to use as pictures. Empty means the source supplies none.
	PictureField string `json:"pictureField,omitempty"`
	// ExcludedLabels are labels that organise the wiki and are not keywords.
	ExcludedLabels []string `json:"excludedLabels,omitempty"`
	// LinkInShop says whether the shop may show "more in the wiki" under a
	// description. Off by default: the address of a page is itself something
	// the product owner may not have meant every orderer to see.
	LinkInShop bool `json:"linkInShop,omitempty"`
	// CreatedAt and UpdatedAt are the server's Unix nanoseconds.
	CreatedAt int64 `json:"createdAt,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// idShape is what a source id may look like: a slug, because it is typed into a
// configuration, shown in a binding and named in a log line.
var idShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Problem returns why a source cannot be stored, or "" when it can.
//
// It is a statement about the record alone. Whether the credential resolves and
// whether the account's standing is right are questions for the source, asked by
// the check route and before every fetch — a configuration refused because the
// wiki was down at the moment somebody saved it would be a configuration nobody
// can write during an outage.
func (s Source) Problem() string {
	switch {
	case !idShape.MatchString(s.ID):
		return "a content source id is lower-case letters, digits and hyphens, at most 63, starting with a letter or digit"
	case strings.TrimSpace(s.Name) == "":
		return "a content source needs a name a product manager can choose it by"
	case !slices.Contains(Kinds, s.Kind):
		return fmt.Sprintf("kind %q is not a content source this server can read; it reads %s",
			s.Kind, strings.Join(Kinds, ", "))
	case strings.TrimSpace(s.CredentialsRef) == "":
		return "a content source needs a credentialsRef naming the secret that holds its read token"
	}
	if why := baseURLProblem(s.BaseURL); why != "" {
		return why
	}
	for _, l := range s.ExcludedLabels {
		if strings.TrimSpace(l) == "" {
			return "an excluded label is blank"
		}
	}
	return ""
}

// baseURLProblem holds a base URL to https and to an address with nothing in it
// a later request could be confused by: no credentials, no query, no fragment.
func baseURLProblem(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case err != nil || u.Host == "":
		return "baseURL must be an absolute https address"
	case u.Scheme != "https":
		// The token travels in a header on every request; over plain http it is
		// readable by every hop in between.
		return "baseURL must be https: the read token travels with every request"
	case u.User != nil:
		return "baseURL must not carry credentials; name the token as credentialsRef"
	case u.RawQuery != "" || u.Fragment != "":
		return "baseURL must not carry a query or a fragment"
	}
	return ""
}

// Normalized is the source as it is stored: trimmed, the base URL without a
// trailing slash, the excluded labels lower-cased and sorted once each.
func (s Source) Normalized() Source {
	s.Name = strings.TrimSpace(s.Name)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.CredentialsRef = strings.TrimSpace(s.CredentialsRef)
	s.PictureField = strings.TrimSpace(s.PictureField)
	var labels []string
	for _, l := range s.ExcludedLabels {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	slices.Sort(labels)
	s.ExcludedLabels = labels
	return s
}

// Excluded reports whether a label is one of the source's excluded labels.
func (s Source) Excluded(label string) bool {
	return slices.Contains(s.ExcludedLabels, strings.ToLower(strings.TrimSpace(label)))
}

// Store holds the configured sources, one JSON file each. Like every design-time
// store it is the run loop's to write (I3).
type Store struct {
	sources *sidecar.Store[Source]
}

// NewStore opens (creating if needed) the directory backing the sources.
func NewStore(dir string) (*Store, error) {
	st, err := sidecar.NewStore(dir, "contentsourcestore",
		func(s Source) string { return s.ID },
		sidecar.Order(func(a, b Source) bool { return a.ID < b.ID }))
	if err != nil {
		return nil, err
	}
	return &Store{sources: st}, nil
}

// Save writes a source, replacing any with the same id.
func (s *Store) Save(src Source) error { return s.sources.Save(src) }

// Source reads one source. A missing one is not an error.
func (s *Store) Source(id string) (Source, bool, error) { return s.sources.Get(id) }

// Sources lists every source, by id.
func (s *Store) Sources() ([]Source, error) { return s.sources.LoadAll() }

// Delete removes a source. Removing one that is not there is not an error.
func (s *Store) Delete(id string) error { return s.sources.Delete(id) }

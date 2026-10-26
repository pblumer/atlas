package contentsource

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/runloop"
)

// Registry is how the rest of Atlas reaches a configured source: the record from
// the store, the token from the secret store, and the adapter for its kind.
//
// The first two are the run loop's (I3 — the vault and the stores are written
// there); dialling and every request after it are not, and must not be. So every
// method here makes its own visit to the loop and none may be called from inside
// one: Do is a rendezvous, and a nested one deadlocks.
type Registry struct {
	loop  *runloop.Loop
	store *Store
	// secret resolves a credentialsRef on the loop, as every Worker's does
	// (Server.resolveConnectorSecret). Empty means it resolves to nothing.
	secret func(ref string) string
	// Transport is the HTTP transport adapters use; nil is the process-wide one.
	// Tests set a test server's.
	Transport http.RoundTripper
	// Dial builds the adapter for a source. Nil dials by kind; tests replace it.
	Dial func(src Source, token string, transport http.RoundTripper) (Adapter, error)
	// changed is told when a source is written, so the recheck runner picks up
	// a new source or lets go of a removed one at once rather than at its next
	// tick.
	changed func()
}

// NewRegistry builds the registry over a store. secret runs on the loop.
func NewRegistry(loop *runloop.Loop, store *Store, secret func(ref string) string) *Registry {
	return &Registry{loop: loop, store: store, secret: secret}
}

// OnChange registers what to tell when a source is written or removed.
func (r *Registry) OnChange(fn func()) { r.changed = fn }

func (r *Registry) notify() {
	if r.changed != nil {
		r.changed()
	}
}

// Sources lists the configured sources. Off the loop only.
func (r *Registry) Sources() ([]Source, error) {
	var (
		out []Source
		err error
	)
	r.loop.Do(func() { out, err = r.store.Sources() })
	return out, err
}

// Configured reports whether any source is configured — the switch the whole
// feature hangs on (§ 3: off until an administrator configures one). Off the
// loop only.
func (r *Registry) Configured() (bool, error) {
	all, err := r.Sources()
	return len(all) > 0, err
}

// ErrNoSource is a binding naming a source that is not configured.
var ErrNoSource = errors.New("no such content source")

// Open reads a source and its token and dials its adapter. Off the loop only.
//
// A token that resolves to nothing is an error naming the reference and never a
// value — there is none to name, and the sentence must not change when there is.
func (r *Registry) Open(id string) (Source, Adapter, error) {
	var (
		src   Source
		found bool
		token string
		err   error
	)
	r.loop.Do(func() {
		if src, found, err = r.store.Source(id); err != nil || !found {
			return
		}
		token = r.secret(src.CredentialsRef)
	})
	switch {
	case err != nil:
		return Source{}, nil, err
	case !found:
		return Source{}, nil, fmt.Errorf("%w: %s", ErrNoSource, id)
	case LooksLikeCredential(src.CredentialsRef):
		// Refused whether or not a secret happens to carry that name, and said
		// without the reference: this message reaches product managers, and a
		// reference that is a pasted token would be shown to every one of them.
		return src, nil, fmt.Errorf("content source %s: its credentialsRef holds what looks like a token, "+
			"not the name of a vault secret; an administrator must put the token into the vault and name the secret", id)
	case token == "":
		return src, nil, fmt.Errorf("content source %s: the vault holds no secret under its credentialsRef; "+
			"an administrator must add it (Console › Workers)", id)
	}
	dial := r.Dial
	if dial == nil {
		dial = DialKind
	}
	ad, err := dial(src, token, r.Transport)
	if err != nil {
		return src, nil, fmt.Errorf("content source %s: %w", id, err)
	}
	return src, ad, nil
}

// DialKind builds the adapter for a source's kind.
func DialKind(src Source, token string, transport http.RoundTripper) (Adapter, error) {
	switch src.Kind {
	case KindWikiWikiBangBang:
		return NewWikiWikiBangBang(src.BaseURL, token, transport)
	}
	return nil, fmt.Errorf("no adapter for kind %q", src.Kind)
}

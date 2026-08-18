package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// gitBinding ties one application to one git repository (ADR-0134 Phase 4). It is
// design-time configuration, stored beside the application rather than on it, so a
// server that never uses git carries none of it.
//
// Like a deployment target (ADR-0129) and a managed connector (ADR-0041), it holds
// only a *reference* to its credential: CredentialRef names a vault entry
// (ADR-0069/0070). The token is resolved for one operation, used, and never
// written anywhere.
//
// LastCommit is the load-bearing field. It is the commit Atlas last saw — the one
// it imported from or pushed — and it is what makes "fast-forward or refuse" real:
// a commit is only allowed when the remote branch still points there, because
// committing on top of a branch that moved underneath would be last-writer-wins on
// content, which ADR-0134 refuses.
type gitBinding struct {
	ApplicationID string `json:"applicationId"`
	RepoURL       string `json:"repoUrl"`
	Branch        string `json:"branch"`
	CredentialRef string `json:"credentialRef,omitempty"`
	LastCommit    string `json:"lastCommit,omitempty"`
	LastSyncAt    int64  `json:"lastSyncAt,omitempty"`
	LastAction    string `json:"lastAction,omitempty"` // "import" or "commit"
	BoundAt       int64  `json:"boundAt"`
	BoundBy       string `json:"boundBy,omitempty"`
}

// Repository URL schemes, split by what each one costs to allow.
const (
	gitSchemeHTTPS = "https"
	gitSchemeHTTP  = "http"
	gitSchemeFile  = "file"
)

// validateRepoURL checks a repository URL and returns it normalized, along with
// whether it addresses the server's own filesystem.
//
// The rules, and why:
//
//   - **https is the transport.** A credential presented over http:// is handed to
//     anyone on the path, so plaintext is refused except for loopback, which is
//     what a single-host setup and the tests need and which does not cross a
//     network. This is the same rule ADR-0129 applies to deployment targets, and
//     there is deliberately no "skip TLS verification" option here either.
//   - **ssh is refused, for now.** go-git can speak it, but doing so safely means
//     host-key verification and a known_hosts policy, and a binding that silently
//     accepts any host key is worse than one that says it cannot do ssh yet.
//   - **file:// is allowed but privileged.** It is what a local or air-gapped
//     remote and the tests use. It is also the one scheme that reaches the server's
//     own disk, so the handler requires admin for it — binding to a local path is
//     an operator act, not an application-editor act.
func validateRepoURL(raw string) (normalized string, isLocal bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("repository URL is required")
	}
	// scp-like syntax (git@host:path) is ssh in disguise; name it as such rather
	// than failing to parse it as a URL.
	if !strings.Contains(raw, "://") && strings.Contains(raw, ":") {
		return "", false, fmt.Errorf("ssh remotes are not supported yet; use an https:// URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("repository URL is not a valid URL: %w", err)
	}
	switch u.Scheme {
	case gitSchemeHTTPS:
		if u.Host == "" {
			return "", false, fmt.Errorf("repository URL must include a host")
		}
	case gitSchemeHTTP:
		if u.Host == "" {
			return "", false, fmt.Errorf("repository URL must include a host")
		}
		if !isLoopbackHost(u.Hostname()) {
			return "", false, fmt.Errorf("repository URL must use https (plaintext is allowed only for loopback)")
		}
	case gitSchemeFile:
		if u.Path == "" {
			return "", false, fmt.Errorf("file:// repository URL must include a path")
		}
		return u.String(), true, nil
	case "ssh", "git":
		return "", false, fmt.Errorf("ssh remotes are not supported yet; use an https:// URL")
	default:
		return "", false, fmt.Errorf("repository URL must use https")
	}
	return strings.TrimRight(u.String(), "/"), false, nil
}

// defaultGitBranch is used when a binding names no branch. It is a plain default,
// not a discovery of the remote's HEAD: naming the branch explicitly is what makes
// "the branch moved" a sentence an operator can act on.
const defaultGitBranch = "main"

// gitBindingStore is a durable store for git bindings, one JSON file per
// application id — the same sidecar approach as every other design-time store
// (ADR-0019/0021/0034). Owned solely by the run-loop goroutine, so it needs no
// locking of its own.
type gitBindingStore struct {
	dir string
}

func newGitBindingStore(dir string) (*gitBindingStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("gitbindingstore: create dir: %w", err)
	}
	return &gitBindingStore{dir: dir}, nil
}

func (g *gitBindingStore) fileFor(appID string) string {
	return filepath.Join(g.dir, hex.EncodeToString([]byte(appID))+".json")
}

func (g *gitBindingStore) save(rec gitBinding) error {
	return atomicWriteJSON(g.dir, g.fileFor(rec.ApplicationID), rec)
}

func (g *gitBindingStore) get(appID string) (gitBinding, bool, error) {
	data, err := os.ReadFile(g.fileFor(appID))
	if err != nil {
		if os.IsNotExist(err) {
			return gitBinding{}, false, nil
		}
		return gitBinding{}, false, fmt.Errorf("gitbindingstore: read: %w", err)
	}
	var rec gitBinding
	if err := json.Unmarshal(data, &rec); err != nil {
		return gitBinding{}, false, fmt.Errorf("gitbindingstore: decode: %w", err)
	}
	return rec, true, nil
}

// delete removes a binding. A missing binding is not an error (idempotent unbind).
func (g *gitBindingStore) delete(appID string) error {
	if err := os.Remove(g.fileFor(appID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("gitbindingstore: remove: %w", err)
	}
	return fsyncDir(g.dir)
}

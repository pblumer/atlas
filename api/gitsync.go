package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// The git transport for the curated source layout (ADR-0134 Phase 4). The layout
// itself lives in appsource.go and knows nothing about git; this file only moves
// it, so the upload endpoint and a git remote import through exactly the same
// rules and there is no second import path to keep in step.
//
// Three properties shape the implementation:
//
//   - **In memory, not on disk.** The repository is cloned into a memory storer
//     and a memory filesystem, so there is no working copy to leave behind, no
//     stale-checkout failure mode, and no scratch directory to secure. The cost is
//     that a very large repository is held in RAM — acceptable for the handful of
//     diagrams an application holds, and the limit ADR-0134 already named.
//   - **Atlas never merges.** A commit is refused unless the remote branch still
//     points at the commit Atlas last saw. Divergence is reported with both
//     commits named; resolving it is a human's job in a real git client.
//   - **Atlas owns only its own paths.** A commit rewrites `atlas.json`,
//     `processes/`, and `forms/` and leaves every other file in the repository
//     alone, so a repository may hold a README, CI configuration, or anything else
//     without Atlas deleting it.
//
// Git I/O runs off the run loop (ADR-0002): state is resolved on the loop, the
// network work happens off it, and the result is recorded back on it — the same
// three-phase shape ADR-0129's promotion uses.

// gitSyncTimeout bounds one import or commit, including the network round trips.
// A repository that cannot answer in two minutes is reported as unreachable
// rather than holding a request open indefinitely.
const gitSyncTimeout = 120 * time.Second

// gitStatusTimeout bounds the best-effort remote read behind the status view. It
// is short because the view is meant to be quick: an unreachable remote is
// reported as unreachable, not waited for.
const gitStatusTimeout = 15 * time.Second

// gitRemote is everything one git operation needs, gathered on the run loop so the
// I/O below never touches a store.
type gitRemote struct {
	URL        string
	Branch     string
	Credential string // resolved from the vault, held only for this operation
}

// auth builds the go-git credential for this remote. A token is presented as HTTP
// basic auth, which is how forge tokens (GitHub, GitLab, Gitea) are carried; the
// username is a placeholder those forges ignore. A local file:// remote takes no
// credential.
func (g gitRemote) auth() transport.AuthMethod {
	if g.Credential == "" || strings.HasPrefix(g.URL, "file://") {
		return nil
	}
	return &githttp.BasicAuth{Username: "atlas", Password: g.Credential}
}

// branchRef is the remote branch this binding tracks.
func (g gitRemote) branchRef() plumbing.ReferenceName {
	branch := g.Branch
	if branch == "" {
		branch = defaultGitBranch
	}
	return plumbing.NewBranchReferenceName(branch)
}

// errEmptyRepo marks a remote that exists but holds no commits yet — the state a
// freshly created repository is in. It is not a failure: it is precisely the case
// where the first commit may proceed without an import, because there is nothing
// there to overwrite.
var errEmptyRepo = errors.New("repository has no commits yet")

// cloneBranch clones one branch of a remote into memory and returns the repository
// with its worktree.
//
// An empty remote — a freshly created repository with no commits — comes back as
// errEmptyRepo, unless seedEmpty is set, in which case an empty repository is
// prepared in memory with HEAD pointed at the branch so the first commit creates
// it. Checking the branch out is not an option there: there is no commit to
// branch *from* yet, which is what "empty" means.
func cloneBranch(ctx context.Context, remote gitRemote, seedEmpty bool) (*git.Repository, billy.Filesystem, error) {
	fsys := memfs.New()
	repo, err := git.CloneContext(ctx, memory.NewStorage(), fsys, &git.CloneOptions{
		URL:           remote.URL,
		Auth:          remote.auth(),
		ReferenceName: remote.branchRef(),
		SingleBranch:  true,
	})
	switch {
	case err == nil:
		return repo, fsys, nil
	case !errors.Is(err, transport.ErrEmptyRemoteRepository):
		return nil, nil, err
	case !seedEmpty:
		return nil, nil, errEmptyRepo
	}

	fsys = memfs.New()
	if repo, err = git.Init(memory.NewStorage(), fsys); err != nil {
		return nil, nil, err
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remote.URL}}); err != nil {
		return nil, nil, err
	}
	if err := repo.Storer.SetReference(
		plumbing.NewSymbolicReference(plumbing.HEAD, remote.branchRef())); err != nil {
		return nil, nil, err
	}
	return repo, fsys, errEmptyRepo
}

// readSourceTreeFrom walks a checked-out worktree and returns it as a source tree.
// Only the paths the layout owns are read — the manifest, `processes/`, `forms/` —
// so a repository that also holds a README or CI configuration imports cleanly and
// those files never reach the artifact stores. The same caps the upload endpoint
// applies bound what a hostile or accidental repository can ask this server to
// hold in memory.
func readSourceTreeFrom(fsys billy.Filesystem) ([]sourceFile, error) {
	var (
		out   []sourceFile
		total int
	)
	// The layout has exactly three locations, so they are read by name rather than
	// discovered: a directory that is not there simply has nothing in it, and a
	// `processes/legacy/` somebody added is not walked into at all.
	for _, dir := range []string{".", "processes", "forms"} {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			continue // absent, which is not a failure — the tree just lacks that part
		}
		for _, e := range entries {
			name := path.Join(dir, e.Name())
			if e.IsDir() || !ownedTreePath(name) {
				continue
			}
			if len(out) >= maxSourceEntries {
				return nil, fmt.Errorf("repository has too many source files")
			}
			data, err := readWholeFile(fsys, name, maxSourceBytes-total)
			if err != nil {
				return nil, err
			}
			total += len(data)
			out = append(out, sourceFile{Path: name, Data: data})
		}
	}
	// Deterministic order, so a tree read from git and one built from the stores
	// compare as bytes without either side having to sort first.
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// readWholeFile reads one worktree file under a remaining-bytes budget.
func readWholeFile(fsys billy.Filesystem, name string, budget int) ([]byte, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("repository source tree is larger than %d bytes", maxSourceBytes)
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(budget)))
}

// ownedTreePath reports whether a file belongs to the curated layout. Everything
// else in the repository is somebody else's and is neither read nor rewritten.
func ownedTreePath(name string) bool {
	name = strings.TrimPrefix(name, "./")
	if name == manifestPath {
		return true
	}
	dir, file := path.Split(name)
	// Exactly one level deep: the layout has no nested directories, so
	// `processes/legacy/old.bpmn` is somebody else's file that happens to sit under
	// a name Atlas uses.
	switch dir {
	case "processes/":
		return strings.HasSuffix(file, ".bpmn")
	case "forms/":
		return strings.HasSuffix(file, ".form.json")
	}
	return false
}

// gitFetchResult is one import's outcome at the transport level.
type gitFetchResult struct {
	Files  []sourceFile
	Commit string
}

// fetchSourceTree clones the branch and reads the layout out of it.
func fetchSourceTree(ctx context.Context, remote gitRemote) (gitFetchResult, error) {
	repo, fsys, err := cloneBranch(ctx, remote, false)
	if err != nil {
		return gitFetchResult{}, err
	}
	head, err := repo.Head()
	if err != nil {
		return gitFetchResult{}, err
	}
	files, err := readSourceTreeFrom(fsys)
	if err != nil {
		return gitFetchResult{}, err
	}
	return gitFetchResult{Files: files, Commit: head.Hash().String()}, nil
}

// gitDivergedError reports a branch that moved since Atlas last saw it. It names
// both commits, because "it diverged" without saying from what to what is not
// something an operator can act on.
type gitDivergedError struct {
	Expected string
	Actual   string
}

func (e gitDivergedError) Error() string {
	if e.Expected == "" {
		return fmt.Sprintf("the branch already has commits (at %s) that Atlas has never seen; import first", short(e.Actual))
	}
	return fmt.Sprintf("the branch moved from %s to %s since the last sync; import first, then commit", short(e.Expected), short(e.Actual))
}

// short renders a commit hash the way a human reads one.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// commitSourceTree writes a source tree into the remote branch as one commit.
//
// It refuses unless the branch still points at expected — the commit Atlas last
// saw. That is the whole of "fast-forward or refuse": Atlas does not merge, does
// not rebase, and does not write conflict markers into files it will later parse.
// An empty expected is only allowed against a repository that has no commits at
// all, because there is then nothing anyone could lose.
func commitSourceTree(ctx context.Context, remote gitRemote, files []sourceFile, expected, message string, author object.Signature) (string, error) {
	repo, fsys, err := cloneBranch(ctx, remote, true)
	switch {
	case errors.Is(err, errEmptyRepo):
		// Nothing there to overwrite — unless Atlas holds a commit the remote no
		// longer has, which means the branch was reset or the repository replaced.
		// Say so rather than quietly re-seeding it.
		if expected != "" {
			return "", gitDivergedError{Expected: expected, Actual: ""}
		}
	case err != nil:
		return "", err
	default:
		head, err := repo.Head()
		if err != nil {
			return "", err
		}
		if actual := head.Hash().String(); actual != expected {
			return "", gitDivergedError{Expected: expected, Actual: actual}
		}
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := replaceOwnedPaths(wt, fsys, files); err != nil {
		return "", err
	}
	status, err := wt.Status()
	if err != nil {
		return "", err
	}
	if status.IsClean() {
		// Nothing changed: report the commit that is already there rather than
		// minting an empty one. A repository full of no-op commits is noise a
		// reviewer has to read past.
		if head, err := repo.Head(); err == nil {
			return head.Hash().String(), nil
		}
	}
	hash, err := wt.Commit(message, &git.CommitOptions{Author: &author})
	if err != nil {
		return "", err
	}
	err = repo.PushContext(ctx, &git.PushOptions{
		Auth:       remote.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{config.RefSpec(
			remote.branchRef() + ":" + remote.branchRef())},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", err
	}
	return hash.String(), nil
}

// replaceOwnedPaths makes the worktree's layout match files exactly: every path
// Atlas owns is removed first, so an artifact deleted in Atlas disappears from the
// repository, and everything else in the tree is left untouched.
func replaceOwnedPaths(wt *git.Worktree, fsys billy.Filesystem, files []sourceFile) error {
	existing, err := readSourceTreeFrom(fsys)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, f := range files {
		wanted[f.Path] = true
	}
	for _, f := range existing {
		if wanted[f.Path] {
			continue
		}
		if _, err := wt.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", f.Path, err)
		}
	}
	for _, f := range files {
		// util.WriteFile creates the parent directory, writes, and closes as one
		// operation, so the whole write is one thing that either happened or did not.
		if err := util.WriteFile(fsys, f.Path, f.Data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		if _, err := wt.Add(f.Path); err != nil {
			return fmt.Errorf("add %s: %w", f.Path, err)
		}
	}
	return nil
}

// remoteHead reads the branch's current commit without cloning it — one
// advertisement exchange, which is what makes the status view cheap enough to run
// on opening a tab. An empty repository reports an empty hash and no error.
func remoteHead(ctx context.Context, remote gitRemote) (string, error) {
	repo := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin", URLs: []string{remote.URL},
	})
	refs, err := repo.ListContext(ctx, &git.ListOptions{Auth: remote.auth()})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	want := remote.branchRef()
	for _, ref := range refs {
		if ref.Name() == want {
			return ref.Hash().String(), nil
		}
	}
	return "", fmt.Errorf("the remote has no branch %q", strings.TrimPrefix(string(want), "refs/heads/"))
}

// gitAuthor renders the commit author for a request. The person who pressed the
// button is who the history should name; an unauthenticated open server has no
// principal to name, so the server itself signs.
func gitAuthor(p *Principal, now time.Time) object.Signature {
	name, email := "Atlas", "atlas@localhost"
	if p != nil && p.Username != "" {
		name = p.Username
		email = p.Username + "@atlas.local"
	}
	return object.Signature{Name: name, Email: email, When: now}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// The HTTP face of the git binding (ADR-0134 Phase 4). Five operations: bind,
// unbind, status, import, commit.
//
// Commits are **explicit**. Atlas does not commit when a diagram is saved: a
// history with one commit per keystroke is a history nobody reads, and the point
// of a repository is a record somebody can review. An operator says when a change
// is worth a commit, and says what it is for in the message.
//
// Every operation follows the ADR-0129 three-phase shape — resolve state on the
// run loop, do the network I/O off it, record the result back on it — so git,
// which can block for as long as a remote is slow, never holds the writer.

// gitBindingView is what the Modeler sees. The credential appears only as its
// vault handle, never resolved (ADR-0041/0069).
//
// RemoteCommit and RemoteError are best-effort: the status view asks the remote
// where its branch is, and an unreachable remote is reported as unreachable
// rather than failing the whole read — the same posture ADR-0129's per-target
// status takes.
type gitBindingView struct {
	Bound         bool   `json:"bound"`
	RepoURL       string `json:"repoUrl,omitempty"`
	Branch        string `json:"branch,omitempty"`
	CredentialRef string `json:"credentialRef,omitempty"`
	LastCommit    string `json:"lastCommit,omitempty"`
	LastSyncAt    int64  `json:"lastSyncAt,omitempty"`
	LastAction    string `json:"lastAction,omitempty"`
	BoundAt       int64  `json:"boundAt,omitempty"`
	RemoteCommit  string `json:"remoteCommit,omitempty"`
	RemoteError   string `json:"remoteError,omitempty"`
	// Diverged is true when the remote branch has moved since Atlas last saw it, so
	// a commit would be refused. It is the one thing the view exists to answer.
	Diverged bool `json:"diverged"`
}

func viewOfBinding(rec gitBinding, bound bool) gitBindingView {
	if !bound {
		return gitBindingView{Bound: false}
	}
	return gitBindingView{
		Bound: true, RepoURL: rec.RepoURL, Branch: rec.Branch,
		CredentialRef: rec.CredentialRef, LastCommit: rec.LastCommit,
		LastSyncAt: rec.LastSyncAt, LastAction: rec.LastAction, BoundAt: rec.BoundAt,
	}
}

// handleSetGitBinding binds an application to a repository, or updates the
// binding. Body: {"repoUrl","branch","credentialRef"}.
//
// Rebinding to a *different* repository or branch clears LastCommit: the recorded
// commit describes the branch Atlas was tracking, and carrying it across would
// make the next commit compare against a hash from another history.
func (s *Server) handleSetGitBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleEditor)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		RepoURL       string `json:"repoUrl"`
		Branch        string `json:"branch"`
		CredentialRef string `json:"credentialRef"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	url, isLocal, err := validateRepoURL(payload.RepoURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A file:// remote is the one scheme that reaches this server's own disk, so it
	// takes the operator role rather than the application's editor role.
	if isLocal && !s.requireAdmin(w, r) {
		return
	}
	branch := strings.TrimSpace(payload.Branch)
	if branch == "" {
		branch = defaultGitBranch
	}

	rec := gitBinding{
		ApplicationID: id, RepoURL: url, Branch: branch,
		CredentialRef: strings.TrimSpace(payload.CredentialRef),
		BoundAt:       time.Now().Unix(),
	}
	if p := principalFrom(r.Context()); p != nil {
		rec.BoundBy = p.UserID
	}
	var saveErr error
	s.do(func() {
		if prev, ok, err := s.gitBindings.get(id); err != nil {
			saveErr = err
			return
		} else if ok {
			rec.BoundAt, rec.BoundBy = prev.BoundAt, prev.BoundBy
			if prev.RepoURL == rec.RepoURL && prev.Branch == rec.Branch {
				rec.LastCommit, rec.LastSyncAt, rec.LastAction = prev.LastCommit, prev.LastSyncAt, prev.LastAction
			}
		}
		saveErr = s.gitBindings.save(rec)
	})
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "save binding: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfBinding(rec, true))
}

// handleDeleteGitBinding unbinds an application. The repository is untouched —
// unbinding is Atlas forgetting about it, not a deletion.
func (s *Server) handleDeleteGitBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleEditor); code != 0 {
		writeError(w, code, msg)
		return
	}
	var delErr error
	s.do(func() { delErr = s.gitBindings.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "unbind: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetGitBinding reports the binding and, best-effort, where the remote
// branch currently is — which is what tells an operator whether a commit would go
// through or be refused.
func (s *Server) handleGetGitBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleViewer); code != 0 {
		writeError(w, code, msg)
		return
	}
	var (
		rec     gitBinding
		bound   bool
		remote  gitRemote
		loadErr error
	)
	s.do(func() {
		rec, bound, loadErr = s.gitBindings.get(id)
		if loadErr != nil || !bound {
			return
		}
		remote = gitRemote{URL: rec.RepoURL, Branch: rec.Branch,
			Credential: s.resolveConnectorSecret(rec.CredentialRef)}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "read binding: "+loadErr.Error())
		return
	}
	out := viewOfBinding(rec, bound)
	if bound {
		ctx, cancel := context.WithTimeout(r.Context(), gitStatusTimeout)
		defer cancel()
		head, err := remoteHead(ctx, remote)
		switch {
		case err != nil:
			out.RemoteError = err.Error()
		default:
			out.RemoteCommit = head
			out.Diverged = head != rec.LastCommit
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// gitImportResp is an import's outcome: what the tree did to the stores, plus the
// commit it came from.
type gitImportResp struct {
	sourceImportResult
	Commit string `json:"commit"`
}

// handleGitImport reads the bound repository's branch into this server. It runs
// the identical path an uploaded archive takes — parseSourceTree, then
// applySourceTree — so git is a transport and not a second set of import rules.
// In particular it does not delete: artifacts the tree omits are reported.
func (s *Server) handleGitImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleEditor)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
		return
	}

	// Phase 1 (on-loop): the binding and its credential.
	remote, bound, err := s.gitRemoteFor(id)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "read binding: "+err.Error())
		return
	case !bound:
		writeError(w, http.StatusBadRequest, "this application is not bound to a repository")
		return
	}

	// Phase 2 (off-loop): the network.
	ctx, cancel := context.WithTimeout(r.Context(), gitSyncTimeout)
	defer cancel()
	fetched, err := fetchSourceTree(ctx, remote)
	switch {
	case errors.Is(err, errEmptyRepo):
		writeError(w, http.StatusConflict, "the repository has no commits yet; commit first to seed it")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "fetch from git: "+err.Error())
		return
	}
	man, byPath, err := parseSourceTree(fetched.Files)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "the repository is not an application source tree: "+err.Error())
		return
	}

	// Phase 3 (on-loop): apply, then record what commit this server now holds.
	ownerID := ""
	if p := principalFrom(r.Context()); p != nil {
		ownerID = p.UserID
	}
	var (
		res      sourceImportResult
		applyErr error
		mismatch string
	)
	s.do(func() {
		mismatch, applyErr = s.adoptOrMatchKey(id, man.Key)
		if mismatch != "" || applyErr != nil {
			return
		}
		res, applyErr = s.applySourceTree(man, byPath, ownerID, func(app project) (int, string) {
			// Belt and braces: the key resolved to this application above, so this only
			// fires if two applications ever answered to one key.
			if app.ID != id {
				return http.StatusConflict, "the repository's key names a different application"
			}
			return s.checkProjectRole(r, app, ScopeRoleEditor)
		})
		if applyErr != nil {
			return
		}
		applyErr = s.recordSync(id, fetched.Commit, "import")
	})
	var refusal sourceRefusal
	switch {
	case mismatch != "":
		writeError(w, http.StatusConflict, mismatch)
	case errors.As(applyErr, &refusal):
		writeError(w, refusal.status, refusal.msg)
	case applyErr != nil:
		writeError(w, http.StatusInternalServerError, "import from git: "+applyErr.Error())
	default:
		writeJSON(w, http.StatusOK, gitImportResp{sourceImportResult: res, Commit: fetched.Commit})
	}
}

// gitCommitResp is a commit's outcome. Changed is false when the repository
// already held exactly this tree — a no-op reported as one rather than as an empty
// commit.
type gitCommitResp struct {
	Commit  string `json:"commit"`
	Changed bool   `json:"changed"`
	Message string `json:"message"`
}

// handleGitCommit writes the application's current source into the bound
// repository as one commit. Body (optional): {"message": "what changed"}.
//
// It is refused when the branch has moved since Atlas last saw it. That refusal is
// the ADR-0134 decision in force: committing on top of a branch that changed
// underneath is last-writer-wins on content, and a plausible-looking overwrite of
// a BPMN diagram is worse than an honest refusal.
func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleEditor)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if code, msg := protectedGuard(proj); code != 0 {
		writeError(w, code, msg)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Message string `json:"message"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = "Update " + proj.Name
	}

	// Phase 1 (on-loop): the binding, its credential, and the tree to write.
	var (
		remote  gitRemote
		bound   bool
		last    string
		files   []sourceFile
		loadErr error
	)
	s.do(func() {
		rec, ok, err := s.gitBindings.get(id)
		if err != nil {
			loadErr = err
			return
		}
		if !ok {
			return
		}
		bound, last = true, rec.LastCommit
		remote = gitRemote{URL: rec.RepoURL, Branch: rec.Branch,
			Credential: s.resolveConnectorSecret(rec.CredentialRef)}
		files, loadErr = s.exportApplicationSource(proj)
	})
	switch {
	case loadErr != nil:
		writeError(w, http.StatusInternalServerError, "prepare commit: "+loadErr.Error())
		return
	case !bound:
		writeError(w, http.StatusBadRequest, "this application is not bound to a repository")
		return
	}

	// Phase 2 (off-loop): the network.
	ctx, cancel := context.WithTimeout(r.Context(), gitSyncTimeout)
	defer cancel()
	author := gitAuthor(principalFrom(r.Context()), time.Now())
	hash, err := commitSourceTree(ctx, remote, files, last, message, author)
	var diverged gitDivergedError
	switch {
	case errors.As(err, &diverged):
		writeError(w, http.StatusConflict, diverged.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "commit to git: "+err.Error())
		return
	}

	// Phase 3 (on-loop): record where the branch now is, so the next commit has
	// something to compare against.
	changed := hash != last
	var saveErr error
	s.do(func() { saveErr = s.recordSync(id, hash, "commit") })
	if saveErr != nil {
		// The commit is pushed and real; only the local record of it failed. Say so
		// rather than reporting a failure that would invite a duplicate attempt.
		writeError(w, http.StatusInternalServerError,
			"the commit was pushed ("+short(hash)+") but recording it locally failed: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, gitCommitResp{Commit: hash, Changed: changed, Message: message})
}

// adoptOrMatchKey reconciles the application a binding names with the key the
// repository carries, and reports a human-readable mismatch when they disagree.
//
// A bound import writes into *this* application and no other. If the two
// disagree, the operator bound the wrong repository — creating a second
// application on the side would leave them with an empty application and a
// binding that never fills it.
//
// An application from before ADR-0134 has no key yet, and adopts the
// repository's: binding it to that repository is the statement that this is the
// application the repository holds. Adoption is refused when another application
// already answers to the key, because two applications sharing one identity would
// make the next clone ambiguous.
//
// Runs on the run loop.
func (s *Server) adoptOrMatchKey(appID, key string) (mismatch string, err error) {
	cur, ok, err := s.projects.get(appID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", sourceRefusal{http.StatusNotFound, "no application with that id"}
	}
	switch {
	case cur.Key == key:
		return "", nil
	case cur.Key != "":
		return "the repository holds a different application (" + key + "); this one is " + cur.Key, nil
	}
	free, err := s.keyIsFree(key, appID)
	if err != nil {
		return "", err
	}
	if !free {
		return "another application already uses the key " + key, nil
	}
	cur.Key, cur.UpdatedAt = key, time.Now().Unix()
	if err := s.projects.save(cur); err != nil {
		return "", err
	}
	return "", nil
}

// recordSync stamps the binding with the commit this server now holds and how it
// got there. It is what the next commit compares against, so a sync that is not
// recorded would look like divergence afterwards.
//
// A binding that vanished mid-operation (unbound in another request) is not an
// error: there is simply nothing left to stamp.
//
// Runs on the run loop.
func (s *Server) recordSync(appID, commit, action string) error {
	rec, ok, err := s.gitBindings.get(appID)
	if err != nil || !ok {
		return err
	}
	rec.LastCommit, rec.LastSyncAt, rec.LastAction = commit, time.Now().Unix(), action
	return s.gitBindings.save(rec)
}

// gitRemoteFor loads an application's binding and resolves its credential, on the
// run loop, returning everything the off-loop I/O needs.
func (s *Server) gitRemoteFor(id string) (gitRemote, bool, error) {
	var (
		out   gitRemote
		bound bool
		err   error
	)
	s.do(func() {
		var rec gitBinding
		rec, bound, err = s.gitBindings.get(id)
		if err != nil || !bound {
			return
		}
		out = gitRemote{URL: rec.RepoURL, Branch: rec.Branch,
			Credential: s.resolveConnectorSecret(rec.CredentialRef)}
	})
	return out, bound, err
}

package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// Deployment targets and promotion (ADR-0129, sending half).
//
// Publishing makes a release *here* (ADR-0128). Promoting puts that same frozen
// release on another Atlas: the artifacts are read back from the deployment records
// the release points at, so what travels is exactly what shipped, not whatever the
// drafts happen to say now.
//
// Two rules shape the code below:
//
//   - **The outbound call never runs on the run loop.** State is resolved on the
//     loop, the HTTP requests happen off it, and the learned bindings are recorded
//     back on it. A peer that hangs must not stall the single writer (I3/ADR-0002).
//   - **Promotion across several targets is not atomic and does not pretend to be.**
//     Each target succeeds or fails on its own and the response says which; there is
//     no distributed transaction between independent engines, and faking one is far
//     beyond what the problem warrants.

// promoteTimeout bounds one outbound promotion. Generous enough for a
// multi-megabyte bundle over a slow link, short enough that an unresponsive peer
// does not hold the request open indefinitely.
const promoteTimeout = 60 * time.Second

// targetStatusTimeout bounds one peer status read. Much shorter than a promotion:
// this is a display refresh, and a slow peer must not hold the view open.
const targetStatusTimeout = 10 * time.Second

// targetView is a target as returned to a caller: configuration and learned
// bindings, never a resolved credential. CredentialRef is a vault *handle*, not a
// secret, and is shown so an operator can see which entry a target uses.
type targetView struct {
	deploymentTarget
}

// promoteResult is one target's outcome. RemoteStatus is the peer's HTTP status
// when it answered at all, so a refusal ("already imported") reads differently
// from an unreachable host.
type promoteResult struct {
	TargetID            string `json:"targetId"`
	TargetName          string `json:"targetName"`
	OK                  bool   `json:"ok"`
	Error               string `json:"error,omitempty"`
	RemoteApplicationID string `json:"remoteApplicationId,omitempty"`
	RemoteStatus        int    `json:"remoteStatus,omitempty"`
}

type promoteResp struct {
	ApplicationID string          `json:"applicationId"`
	Version       int32           `json:"version"`
	Results       []promoteResult `json:"results"`
}

// handleCreateTarget registers a peer server. Admin-gated: a target names where
// this server may ship work and which credential it presents, which is operator
// configuration, not application content.
func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Name          string `json:"name"`
		BaseURL       string `json:"baseUrl"`
		Kind          string `json:"kind"`
		CredentialRef string `json:"credentialRef"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "target name is required")
		return
	}
	base, err := validateTargetURL(payload.BaseURL)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	rec := deploymentTarget{
		ID: id, Name: name, BaseURL: base,
		Kind:          strings.TrimSpace(payload.Kind),
		CredentialRef: strings.TrimSpace(payload.CredentialRef),
		CreatedAt:     time.Now().Unix(),
	}
	var saveErr error
	s.do(func() { saveErr = s.targets.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "create target: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, targetView{rec})
}

// handleListTargets lists the configured peers. Readable by any authenticated
// caller — an application editor has to pick a target to promote to — while
// creating and deleting stay admin-only.
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	out := []targetView{}
	var loadErr error
	s.do(func() {
		var recs []deploymentTarget
		if recs, loadErr = s.targets.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			out = append(out, targetView{rec})
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list targets: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleDeleteTarget removes a peer. Idempotent; the applications that were
// promoted to it are untouched, both here and over there.
func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	var delErr error
	s.do(func() { delErr = s.targets.Delete(r.PathValue("id")) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete target: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePromoteRelease ships an existing release to one or more targets.
func (s *Server) handlePromoteRelease(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	version64, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version64 <= 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid release version")
		return
	}
	version := int32(version64)

	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		TargetIDs []string `json:"targetIds"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(payload.TargetIDs) == 0 {
		httpapi.Error(w, http.StatusBadRequest, "at least one targetId is required")
		return
	}

	// Promoting ships this application's work elsewhere, so it needs the editor role
	// on it — the same gate publishing uses (ADR-0071).
	proj, code, msg := s.authorizeProject(r, appID, ScopeRoleEditor)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	// Phase 1 (on-loop): resolve the release, its artifacts, and the targets with
	// their credentials. Everything the outbound calls need is gathered here so the
	// loop is free again before any network I/O starts.
	var (
		bundle     importBundleReq
		targets    []deploymentTarget
		creds      map[string]string
		loadErr    error
		noRelease  bool
		badTargets []string
	)
	s.do(func() {
		rels, err := s.releases.forApplication(appID)
		if err != nil {
			loadErr = err
			return
		}
		var rel *applicationRelease
		for i := range rels {
			if rels[i].Version == version {
				rel = &rels[i]
				break
			}
		}
		if rel == nil {
			noRelease = true
			return
		}

		if bundle = s.bundleForRelease(proj.Name, *rel, &loadErr); loadErr != nil {
			return
		}

		creds = map[string]string{}
		for _, tid := range payload.TargetIDs {
			tgt, ok, err := s.targets.Get(tid)
			if err != nil {
				loadErr = err
				return
			}
			if !ok {
				badTargets = append(badTargets, tid)
				continue
			}
			targets = append(targets, tgt)
			// Resolved on the loop (the vault store's owner) and held only for the
			// duration of this request, never written anywhere (I6).
			creds[tgt.ID] = s.resolveConnectorSecret(tgt.CredentialRef)
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "prepare promotion: "+loadErr.Error())
		return
	case noRelease:
		httpapi.Error(w, http.StatusNotFound, fmt.Sprintf("no release v%d for this application", version))
		return
	case len(badTargets) > 0:
		httpapi.Error(w, http.StatusBadRequest, "unknown target(s): "+strings.Join(badTargets, ", "))
		return
	case len(bundle.Artifacts) == 0:
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf("release v%d carries no deployable artifacts", version))
		return
	}

	// Phase 2 (off-loop): the network. One request per target, each reported on its
	// own — a failure here is that target's failure, not the call's.
	payloadBytes, err := json.Marshal(bundle)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "encode bundle: "+err.Error())
		return
	}
	results := make([]promoteResult, 0, len(targets))
	learned := map[string]string{} // targetID → remote application id
	for _, tgt := range targets {
		res := s.pushBundle(r.Context(), tgt, creds[tgt.ID], payloadBytes)
		results = append(results, res)
		if res.OK && res.RemoteApplicationID != "" {
			learned[tgt.ID] = res.RemoteApplicationID
		}
	}

	// Phase 3 (on-loop): remember where the application lives on each target, so a
	// later status read can address it (ADR-0129 C1).
	if len(learned) > 0 {
		s.do(func() {
			for tid, remoteAppID := range learned {
				tgt, ok, err := s.targets.Get(tid)
				if err != nil || !ok {
					continue // the target was deleted mid-flight; the promotion still happened
				}
				if tgt.Bindings == nil {
					tgt.Bindings = map[string]string{}
				}
				if tgt.Bindings[appID] == remoteAppID {
					continue
				}
				tgt.Bindings[appID] = remoteAppID
				_ = s.targets.Save(tgt) // a lost binding costs a re-learn, not correctness
			}
		})
	}

	httpapi.JSON(w, http.StatusOK, promoteResp{ApplicationID: appID, Version: version, Results: results})
}

// bundleForRelease assembles what travels for one release.
//
// The artifacts come from the *deployment* records the release points at, not from
// the application's current drafts: a release is the frozen thing that shipped, and
// the drafts may have moved on since. Members that are not processes are skipped —
// forms and decisions do not travel yet (ADR-0128 Phase 2 records only processes),
// and silently sending nothing for them is better than sending something the
// receiver cannot deploy.
//
// Must be called on the run-loop goroutine (the deploy store's owner). Errors are
// reported through outErr so the caller's do() closure can bail cleanly.
func (s *Server) bundleForRelease(appName string, rel applicationRelease, outErr *error) importBundleReq {
	bundle := importBundleReq{Application: appName}
	bundle.Release.Version = rel.Version
	bundle.Release.Note = rel.Note
	for _, m := range rel.Members {
		if m.Kind != "process" {
			continue
		}
		rec, ok, err := s.deploys.load(m.Key)
		if err != nil {
			*outErr = err
			return bundle
		}
		if !ok {
			*outErr = fmt.Errorf("release v%d references definition %d, which is no longer stored", rel.Version, m.Key)
			return bundle
		}
		art := importArtifact{Kind: "process", ProcessID: rec.ProcessID, XML: rec.XML}
		art.DMNXMLs = append(art.DMNXMLs, rec.DMNXMLs...)
		if len(art.DMNXMLs) == 0 && rec.DMNXML != "" {
			art.DMNXMLs = append(art.DMNXMLs, rec.DMNXML) // legacy single-model field
		}
		bundle.Artifacts = append(bundle.Artifacts, art)
	}
	return bundle
}

// pushBundle performs one target's outbound request. It never returns an error:
// every failure mode — unreachable, refused, malformed reply — is that target's
// reported outcome, because the caller is promoting to several independent servers
// and one being down says nothing about the others.
// WithTargetTLSRoots trusts an operator's certificate authorities, in addition to
// the host's, when this server calls another Atlas — publishing an application to a
// deployment target, and reading that target's status back.
//
// validateTargetURL demands https of a peer, and a peer on-prem usually presents a
// certificate an internal CA issued, which the host's roots do not know. Without
// this the only answer is the host's trust store, and in a container that is an
// image change. It never replaces the system roots and it is never a way around
// verification — the skip-verify switch this file refuses stays refused — and it
// deliberately reaches no further than a peer Atlas: a worker calling a third
// party keeps the host's roots, because its endpoint is somebody else's
// (ADR-0129, ADR-0191).
func WithTargetTLSRoots(pool *x509.CertPool) Option {
	return func(s *Server) {
		s.targetClient = &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
	}
}

// targetHTTP is the client the two calls to a peer use: the operator's, where they
// named a CA, and otherwise the default one, which verifies against the host's
// roots exactly as this code always has.
func (s *Server) targetHTTP() *http.Client {
	if s.targetClient != nil {
		return s.targetClient
	}
	return http.DefaultClient
}

func (s *Server) pushBundle(ctx context.Context, tgt deploymentTarget, credential string, payload []byte) promoteResult {
	out := promoteResult{TargetID: tgt.ID, TargetName: tgt.Name}

	reqCtx, cancel := context.WithTimeout(ctx, promoteTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tgt.BaseURL+"/api/v1/applications/import", bytes.NewReader(payload))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}

	// The transport verifies TLS, against the host's roots plus whatever --tls-ca
	// added. Nothing here relaxes that.
	resp, err := s.targetHTTP().Do(req)
	if err != nil {
		out.Error = "unreachable: " + err.Error()
		return out
	}
	defer resp.Body.Close()
	out.RemoteStatus = resp.StatusCode

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxXMLBytes))
	if err != nil {
		out.Error = "read reply: " + err.Error()
		return out
	}
	var reply struct {
		ApplicationID string `json:"applicationId"`
		Imported      bool   `json:"imported"`
		Reason        string `json:"reason"`
		Error         string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &reply) // a non-JSON reply leaves the fields empty

	out.RemoteApplicationID = reply.ApplicationID
	switch {
	case resp.StatusCode == http.StatusOK && reply.Imported:
		out.OK = true
	case reply.Reason != "":
		out.Error = reply.Reason
	case reply.Error != "":
		out.Error = reply.Error
	default:
		out.Error = fmt.Sprintf("target refused the bundle (HTTP %d)", resp.StatusCode)
	}
	return out
}

// targetStatus is one peer's answer about this application: whether it has ever
// been promoted there (Bound), whether the peer answered just now (Reachable), and
// what it reports running.
//
// Bound and Reachable are separate on purpose. "Never shipped here" and "shipped
// here but the peer is down" are different operational situations, and collapsing
// them into one blank row would hide an outage behind what looks like an unused
// environment.
type targetStatus struct {
	TargetID    string `json:"targetId"`
	TargetName  string `json:"targetName"`
	BaseURL     string `json:"baseUrl"`
	Bound       bool   `json:"bound"`
	Reachable   bool   `json:"reachable"`
	Error       string `json:"error,omitempty"`
	Version     int32  `json:"version,omitempty"`
	Running     int    `json:"running,omitempty"`
	Finished    int    `json:"finished,omitempty"`
	Definitions int    `json:"definitions,omitempty"`
}

// handleApplicationTargets reports what each configured peer currently runs for
// this application.
//
// It is deliberately a separate endpoint from the local deployments view: this one
// makes an outbound call per bound target, so it is as slow as the slowest peer and
// can partially fail. Folding it into the local read would make every page load
// wait on the network. Reads are best-effort by design — a peer that is down is
// reported as unreachable, never an error that empties the whole view.
func (s *Server) handleApplicationTargets(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, appID, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	// Phase 1 (on-loop): the targets, their bindings for this application, and
	// their credentials.
	var (
		targets []deploymentTarget
		creds   map[string]string
		loadErr error
	)
	s.do(func() {
		if targets, loadErr = s.targets.LoadAll(); loadErr != nil {
			return
		}
		creds = make(map[string]string, len(targets))
		for _, tgt := range targets {
			if tgt.Bindings[appID] != "" {
				creds[tgt.ID] = s.resolveConnectorSecret(tgt.CredentialRef)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list targets: "+loadErr.Error())
		return
	}

	// Phase 2 (off-loop): ask each bound peer.
	out := make([]targetStatus, 0, len(targets))
	for _, tgt := range targets {
		st := targetStatus{TargetID: tgt.ID, TargetName: tgt.Name, BaseURL: tgt.BaseURL}
		remoteID := tgt.Bindings[appID]
		if remoteID == "" {
			// Never promoted here: nothing to ask, and no failure to report.
			out = append(out, st)
			continue
		}
		st.Bound = true
		s.fillRemoteStatus(r.Context(), &st, tgt, remoteID, creds[tgt.ID])
		out = append(out, st)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// fillRemoteStatus asks one peer for its deployments view of an application and
// folds the answer into st. Every failure is recorded on st rather than returned:
// one peer being unreachable says nothing about the others.
func (s *Server) fillRemoteStatus(ctx context.Context, st *targetStatus, tgt deploymentTarget, remoteAppID, credential string) {
	reqCtx, cancel := context.WithTimeout(ctx, targetStatusTimeout)
	defer cancel()

	url := tgt.BaseURL + "/api/v1/applications/" + remoteAppID + "/deployments"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		st.Error = err.Error()
		return
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := s.targetHTTP().Do(req)
	if err != nil {
		st.Error = "unreachable: " + err.Error()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A deploy token may not read this endpoint (it is allowlisted to import
		// only), so say what happened rather than showing a blank row.
		st.Error = fmt.Sprintf("peer answered HTTP %d", resp.StatusCode)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXMLBytes))
	if err != nil {
		st.Error = "read reply: " + err.Error()
		return
	}
	var view applicationDeploymentsResp
	if err := json.Unmarshal(body, &view); err != nil {
		st.Error = "unreadable reply from peer"
		return
	}
	st.Reachable = true
	st.Version = view.Version
	st.Running = view.Running
	st.Finished = view.Finished
	st.Definitions = len(view.Definitions)
}

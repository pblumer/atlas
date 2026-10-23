package identity

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The identity area's routes. Who may call them is the route table's (api/openapi.go):
// everything here is reachable by any signed-in user, and each handler narrows that to
// the caller's own connections, or an administrator.

// view is what a caller is told about their identities and connections. It carries no
// credential and no credential reference: the record has none to carry.
type view struct {
	Identities  []ExternalIdentity `json:"identities"`
	Connections []Connection       `json:"connections"`
	// Linkable are the providers this server can link an account with, and where the
	// flow starts; empty when linking is not configured.
	Linkable []LinkOption `json:"linkable"`
}

// LinkOption is one provider a person can link an account with.
type LinkOption struct {
	Provider string `json:"provider"`
	Start    string `json:"start"`
}

// HandleMine lists the caller's own identities and connections.
func (s *Service) HandleMine(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		httpapi.Error(w, http.StatusUnauthorized, "sign in to see your connections")
		return
	}
	ids, conns := s.ForOwner(p.UserID)
	linkable := s.linkable
	if linkable == nil {
		linkable = []LinkOption{}
	}
	httpapi.JSON(w, http.StatusOK, view{Identities: ids, Connections: conns, Linkable: linkable})
}

// SetLinkable says which providers this server links accounts with. Set once, while
// the server is built.
func (s *Service) SetLinkable(options ...LinkOption) { s.linkable = options }

// HandleOfUser lists one user's identities and connections, for an administrator
// answering "what can act as this person".
func (s *Service) HandleOfUser(w http.ResponseWriter, r *http.Request) {
	ids, conns := s.ForOwner(r.PathValue("id"))
	httpapi.JSON(w, http.StatusOK, view{Identities: ids, Connections: conns, Linkable: []LinkOption{}})
}

// HandleRevoke withdraws a connection. Its owner may, and so may an administrator.
func (s *Service) HandleRevoke(isAdmin func(*httpapi.Principal) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := httpapi.PrincipalFrom(r.Context())
		if p == nil || p.UserID == "" {
			httpapi.Error(w, http.StatusUnauthorized, "sign in to revoke a connection")
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := s.decode(r, &body); err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		c, err := s.Revoke(r.PathValue("id"), p.UserID, isAdmin(p), strings.TrimSpace(body.Reason))
		if err != nil {
			writeError(w, err)
			return
		}
		httpapi.JSON(w, http.StatusOK, c)
	}
}

// HandleSetWorkers replaces the Workers allowed to act through a connection. Only the
// owner may: it is their consent.
func (s *Service) HandleSetWorkers(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		httpapi.Error(w, http.StatusUnauthorized, "sign in to change a connection")
		return
	}
	// Consent is given in the owner's own session, never by an application they
	// granted access to their account.
	if !p.Session {
		httpapi.Error(w, http.StatusForbidden, "allowing a Worker to act as you is your own act, given in your own session")
		return
	}
	var body struct {
		Workers []string `json:"workers"`
	}
	if err := s.decode(r, &body); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.SetAllowedWorkers(r.PathValue("id"), p.UserID, body.Workers)
	if err != nil {
		writeError(w, err)
		return
	}
	httpapi.JSON(w, http.StatusOK, c)
}

func (s *Service) decode(r *http.Request, into any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, into); err != nil {
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNotOwner):
		// A connection somebody else owns is answered as one that does not exist for
		// them, so the route cannot be used to learn which ids exist.
		httpapi.Error(w, http.StatusNotFound, ErrNotFound.Error())
	case errors.Is(err, ErrRevoked):
		httpapi.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrUnknownWorker):
		httpapi.Error(w, http.StatusBadRequest, err.Error())
	default:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	}
}

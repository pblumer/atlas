package contentsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// audited records a change to the configuration in the audit trail (ADR-0197),
// with who and from where. Never the credential: only its reference is in the
// record, and only the reference is named here.
func audited(r *http.Request, change string, src Source) {
	attrs := []slog.Attr{slog.String("client_ip", httpapi.ClientIP(r)),
		slog.String("change", change), slog.String("source", src.ID)}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		attrs = append(attrs, slog.String("actor", p.Username), slog.String("actor_id", p.UserID))
	}
	if change != "deleted" {
		attrs = append(attrs, slog.String("base_url", src.BaseURL), slog.String("credentials_ref", SafeRef(src.CredentialsRef)))
	}
	logging.Info(logging.ContentSourceChanged, "a content source was "+change, attrs...)
}

// The administrator's surface: configuring a source (§ 3).
//
// Admin-only at the route table, and there is no object axis below it: a source
// is installation configuration, like a Worker, and it names a credential. A
// product manager chooses among the configured sources on the catalogue screen
// and never reads one of these records.

// Service is the content-source configuration API.
type Service struct {
	reg *Registry
	now func() int64
	// InUse names the products bound to a source, so removing one that still
	// supplies something is refused rather than leaving bindings nothing can
	// recheck. Off the loop. Nil answers none.
	InUse func(id string) ([]string, error)
}

// NewService builds the configuration API over a registry.
func NewService(reg *Registry, now func() int64) *Service {
	return &Service{reg: reg, now: now}
}

// HandleList lists the configured sources.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	all, err := s.reg.Sources()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list content sources: "+err.Error())
		return
	}
	shown := make([]Source, 0, len(all))
	for _, src := range all {
		shown = append(shown, src.Shown())
	}
	httpapi.JSON(w, http.StatusOK, shown)
}

// HandleGet reads one.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		src   Source
		found bool
		err   error
	)
	s.reg.loop.Do(func() { src, found, err = s.reg.store.Source(id) })
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read content source: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no content source "+id)
	default:
		httpapi.JSON(w, http.StatusOK, src.Shown())
	}
}

// HandleCreate configures a new source. An id already taken is a conflict: a
// create that silently replaced a source would rebind every product bound to it.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeSource(w, r)
	if !ok {
		return
	}
	in.CreatedAt, in.UpdatedAt = s.now(), s.now()
	var (
		taken bool
		err   error
	)
	s.reg.loop.Do(func() {
		if _, taken, err = s.reg.store.Source(in.ID); err != nil || taken {
			return
		}
		err = s.reg.store.Save(in)
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save content source: "+err.Error())
	case taken:
		httpapi.Error(w, http.StatusConflict, "content source "+in.ID+" exists already; change it with PUT")
	default:
		audited(r, "created", in)
		s.reg.notify()
		httpapi.JSON(w, http.StatusCreated, in.Shown())
	}
}

// HandleUpdate replaces a source's configuration. The id is the path's.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeSourceAs(w, r, id)
	if !ok {
		return
	}
	var (
		found bool
		err   error
	)
	s.reg.loop.Do(func() {
		var prev Source
		if prev, found, err = s.reg.store.Source(id); err != nil || !found {
			return
		}
		in.CreatedAt, in.UpdatedAt = prev.CreatedAt, s.now()
		err = s.reg.store.Save(in)
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save content source: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no content source "+id)
	default:
		audited(r, "changed", in)
		s.reg.notify()
		httpapi.JSON(w, http.StatusOK, in.Shown())
	}
}

// HandleDelete removes a source no product is bound to.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.InUse != nil {
		bound, err := s.InUse(id)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read bindings: "+err.Error())
			return
		}
		if len(bound) > 0 {
			httpapi.Error(w, http.StatusConflict, "content source "+id+" still supplies "+
				strings.Join(bound, ", ")+"; unlink them first, or their content could not be rechecked")
			return
		}
	}
	var err error
	s.reg.loop.Do(func() { err = s.reg.store.Delete(id) })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete content source: "+err.Error())
		return
	}
	audited(r, "deleted", Source{ID: id})
	s.reg.notify()
	w.WriteHeader(http.StatusNoContent)
}

// checkResult is what the check route answers: whether the source is reachable
// with its credential, and whether the account is what § 4 demands.
type checkResult struct {
	Reachable bool     `json:"reachable"`
	Error     string   `json:"error,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Spaces    []string `json:"spaces,omitempty"`
	Problems  []string `json:"problems,omitempty"`
}

// HandleCheck asks the source for the account's standing, so an administrator
// sees before any product is bound whether the token and the roles are right. It
// answers 200 either way: the result is the answer, not the request's failure.
func (s *Service) HandleCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, ad, err := s.reg.Open(id)
	if err != nil {
		if errors.Is(err, ErrNoSource) {
			httpapi.Error(w, http.StatusNotFound, "no content source "+id)
			return
		}
		httpapi.JSON(w, http.StatusOK, checkResult{Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := ad.Standing(ctx)
	if err != nil {
		httpapi.JSON(w, http.StatusOK, checkResult{Error: err.Error()})
		return
	}
	out := checkResult{Reachable: true, Scope: st.Scope, Problems: CheckStanding(st)}
	for _, g := range st.Spaces {
		if g.Role == RoleViewer {
			out.Spaces = append(out.Spaces, g.Space)
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

func decodeSource(w http.ResponseWriter, r *http.Request) (Source, bool) {
	var in Source
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("malformed JSON body: %v", err))
		return Source{}, false
	}
	in = in.Normalized()
	if why := in.Problem(); why != "" {
		httpapi.Error(w, http.StatusBadRequest, why)
		return Source{}, false
	}
	return in, true
}

func decodeSourceAs(w http.ResponseWriter, r *http.Request, id string) (Source, bool) {
	var in Source
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("malformed JSON body: %v", err))
		return Source{}, false
	}
	if in.ID != "" && in.ID != id {
		httpapi.Error(w, http.StatusBadRequest, "a content source's id cannot change; bindings name it")
		return Source{}, false
	}
	in.ID = id
	in = in.Normalized()
	if why := in.Problem(); why != "" {
		httpapi.Error(w, http.StatusBadRequest, why)
		return Source{}, false
	}
	return in, true
}

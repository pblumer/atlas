package infomodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/token"
)

// maxJSONBytes caps a model document. A class diagram a person can read is small;
// this is generous enough that the cap is never the thing a modeler meets.
const maxJSONBytes = 4 << 20

var errBodyTooLarge = errors.New("request body is too large")

// ApplicationAccess is the caller's resolved access to the process application that
// owns a model. Like Panorama, this area reuses the application scope (ADR-0071/
// 0128) rather than inventing a second ACL.
type ApplicationAccess struct {
	Exists    bool
	CanView   bool
	CanEdit   bool
	Protected bool
}

// AccessResolver resolves application ownership on the API run loop. It is invoked
// only from a loop turn, so it may read the server's application store directly; it
// must not call Loop.Do recursively.
type AccessResolver func(r *http.Request, applicationID string) (ApplicationAccess, error)

// IDGenerator mints an opaque model or element id off the run loop.
type IDGenerator func() (string, error)

// Clock supplies deterministic timestamps in tests.
type Clock func() time.Time

// Service serves the information-model area. Its only mutable dependency is the
// store, and every read and write of it goes through the loop — the design-time
// single-writer boundary (I3, ADR-0147).
type Service struct {
	loop   *runloop.Loop
	store  *Store
	access AccessResolver
	newID  IDGenerator
	now    Clock
}

// New builds the service.
func New(loop *runloop.Loop, store *Store, access AccessResolver, newID IDGenerator, now Clock) *Service {
	return &Service{loop: loop, store: store, access: access, newID: newID, now: now}
}

// HandleSubset serves the authoring subset: what may be created, what may be drawn
// between what, and the statement that it is a subset.
//
// It is served rather than duplicated in the browser, and that is the whole reason
// this route exists — the canvas has to refuse a connection while it is being
// dragged, and the server has to refuse it on write. It reads no model and takes no
// id: the subset is a property of this build, not of anybody's document, so asking
// for it discloses nothing about what exists.
func (s *Service) HandleSubset(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, AuthoringSubset())
}

type createRequest struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Documentation string `json:"documentation"`
}

// HandleCreate starts an empty information model for an application.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var payload createRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	payload.ApplicationID = strings.TrimSpace(payload.ApplicationID)
	if payload.ApplicationID == "" {
		httpapi.Error(w, http.StatusBadRequest, "applicationId is required")
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		httpapi.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	id, err := s.newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint information model id: "+err.Error())
		return
	}
	now := s.now().Unix()
	actor := requestActor(r)
	model := Model{
		ID: id, ApplicationID: payload.ApplicationID, Name: name,
		Documentation: strings.TrimSpace(payload.Documentation), Revision: 1,
		Classes: []Class{}, Associations: []Association{},
		CreatedAt: now, CreatedBy: actor, UpdatedAt: now, UpdatedBy: actor,
	}

	var refusal *operationRefusal
	var opErr error
	s.loop.Do(func() {
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if refusal = writeRefusal(access, true); refusal != nil {
			return
		}
		if _, exists, err := s.store.Get(model.ID); err != nil {
			opErr = err
			return
		} else if exists {
			opErr = fmt.Errorf("generated id collision")
			return
		}
		opErr = s.store.Save(model)
	})
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save information model: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusCreated, summarize(model))
}

// HandleList lists the models the caller may view, newest first.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	applicationID := strings.TrimSpace(r.URL.Query().Get("applicationId"))
	out := []Summary{}
	var opErr error
	s.loop.Do(func() {
		models, err := s.store.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, m := range models {
			if applicationID != "" && m.ApplicationID != applicationID {
				continue
			}
			access, err := s.access(r, m.ApplicationID)
			if err != nil {
				opErr = err
				return
			}
			if access.Exists && access.CanView {
				out = append(out, summarize(m))
			}
		}
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list information models: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// modelResponse is one model with the verdict on it. The findings travel with the
// document rather than behind a separate call, because the canvas marks them the
// moment it draws — a diagram shown clean and corrected a round trip later is a
// diagram that was wrong on screen.
type modelResponse struct {
	Model
	Validation ValidationResult `json:"validation"`
}

// HandleGet returns one whole model, with its validation.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, modelResponse{Model: model, Validation: Validate(model)})
}

type updateRequest struct {
	Name          *string        `json:"name"`
	Documentation *string        `json:"documentation"`
	Classes       *[]Class       `json:"classes"`
	Associations  *[]Association `json:"associations"`
	// Revision is the revision the editor read. A write against a stale one is
	// refused rather than silently overwriting somebody's classes.
	Revision int64 `json:"revision"`
}

// HandleUpdate replaces a model's content.
//
// It takes the whole document rather than a patch, and refuses one that does not
// validate. A canvas edits a graph — moving a class, retyping an attribute,
// redrawing a line — and a patch language for that would be a second way to say
// everything the document already says. Refusing an invalid write is what keeps the
// store's guarantee simple: every model on disk is one the subset accepts, so a
// deploy resolving `itemSubjectRef` against it never meets a half-model.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	var payload updateRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	var (
		refusal   *operationRefusal
		opErr     error
		conflict  bool
		invalid   *ValidationResult
		saved     Model
		validated ValidationResult
	)
	s.loop.Do(func() {
		model, exists, err := s.store.Get(r.PathValue("id"))
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			refusal = &operationRefusal{status: http.StatusNotFound, message: notFound}
			return
		}
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if refusal = writeRefusal(access, false); refusal != nil {
			return
		}
		if payload.Revision != 0 && payload.Revision != model.Revision {
			conflict = true
			return
		}

		next := model
		if payload.Name != nil {
			name := strings.TrimSpace(*payload.Name)
			if name == "" {
				refusal = &operationRefusal{status: http.StatusBadRequest, message: "name cannot be empty"}
				return
			}
			next.Name = name
		}
		if payload.Documentation != nil {
			next.Documentation = strings.TrimSpace(*payload.Documentation)
		}
		if payload.Classes != nil {
			next.Classes = *payload.Classes
		}
		if payload.Associations != nil {
			next.Associations = *payload.Associations
		}
		if next.Classes == nil {
			next.Classes = []Class{}
		}
		if next.Associations == nil {
			next.Associations = []Association{}
		}
		if err := s.assignIDs(&next); err != nil {
			opErr = err
			return
		}

		if res := Validate(next); !res.Valid {
			invalid = &res
			return
		}
		next.Revision = model.Revision + 1
		next.UpdatedAt = s.now().Unix()
		next.UpdatedBy = requestActor(r)
		if opErr = s.store.Save(next); opErr != nil {
			return
		}
		saved = next
		validated = Validate(next)
	})

	switch {
	case refusal != nil:
		httpapi.Error(w, refusal.status, refusal.message)
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save information model: "+opErr.Error())
	case conflict:
		httpapi.Error(w, http.StatusConflict,
			"this model changed since you opened it; reload before saving so the other edit is not lost")
	case invalid != nil:
		httpapi.JSON(w, http.StatusBadRequest, map[string]any{
			"error":    "the model is not valid",
			"findings": invalid.Findings,
		})
	default:
		httpapi.JSON(w, http.StatusOK, modelResponse{Model: saved, Validation: validated})
	}
}

// assignIDs mints an id for every class and association that did not arrive with a
// real one, and rewrites the association ends that referred to it.
//
// Ids are the server's to hand out, but a canvas cannot draw a relationship between
// two boxes it has no way to name — so it gives a new box a local handle and points
// the relationship at that. Anything that is not a minted id is such a handle:
// minted ids are hex tokens, so the test is exact and needs no marker the client
// could get wrong. Remapping here, rather than trusting the client to send ids back
// unchanged, is what keeps every id on disk one this server issued.
func (s *Service) assignIDs(m *Model) error {
	remap := map[string]string{}
	for i := range m.Classes {
		old := strings.TrimSpace(m.Classes[i].ID)
		if old != "" && token.IsHex(old) {
			m.Classes[i].ID = old
			continue
		}
		id, err := s.newID()
		if err != nil {
			return fmt.Errorf("mint class id: %w", err)
		}
		m.Classes[i].ID = id
		if old != "" {
			remap[old] = id
		}
	}
	for i := range m.Associations {
		a := &m.Associations[i]
		if next, ok := remap[a.From.ClassID]; ok {
			a.From.ClassID = next
		}
		if next, ok := remap[a.To.ClassID]; ok {
			a.To.ClassID = next
		}
		if id := strings.TrimSpace(a.ID); id != "" && token.IsHex(id) {
			a.ID = id
			continue
		}
		id, err := s.newID()
		if err != nil {
			return fmt.Errorf("mint association id: %w", err)
		}
		a.ID = id
	}
	return nil
}

// HandleDelete removes a model.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	var refusal *operationRefusal
	var opErr error
	s.loop.Do(func() {
		model, exists, err := s.store.Get(r.PathValue("id"))
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			refusal = &operationRefusal{status: http.StatusNotFound, message: notFound}
			return
		}
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if refusal = writeRefusal(access, false); refusal != nil {
			return
		}
		opErr = s.store.Delete(model.ID)
	})
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete information model: "+opErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleSchema serves the JSON Schema projection of one class — the derived,
// read-only contract a value of that class is checked against, together with what
// the projection could not carry.
func (s *Service) HandleSchema(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	className := strings.TrimSpace(r.URL.Query().Get("class"))
	if className == "" {
		httpapi.Error(w, http.StatusBadRequest, "class is required: a schema describes one class")
		return
	}
	projection, err := SchemaFor(model, className)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, projection)
}

// VocabularyOnLoop is what an application's information models say, flattened for
// resolution — what a deploy and the Problems panel resolve `itemSubjectRef`
// against. It runs inside an existing loop turn, so callers must already hold one.
//
// An application with no model yields an *unmodeled* vocabulary rather than an
// empty one, and the difference is deliberate: the checks that need a vocabulary
// say nothing at all against it, so an instance that has not started modeling does
// not get a warning on every data object it has.
func (s *Service) VocabularyOnLoop(applicationID string) (*Vocabulary, error) {
	if strings.TrimSpace(applicationID) == "" {
		return NewVocabulary(nil), nil
	}
	models, err := s.store.ForApplication(applicationID)
	if err != nil {
		return nil, err
	}
	// The store lists newest first; NewVocabulary lets a later entry win a name
	// clash, so reverse to make the newest model the one that wins.
	for i, j := 0, len(models)-1; i < j; i, j = i+1, j-1 {
		models[i], models[j] = models[j], models[i]
	}
	return NewVocabulary(models), nil
}

const notFound = "no such information model"

func (s *Service) readModel(r *http.Request, id string) (Model, *operationRefusal, error) {
	var model Model
	var refusal *operationRefusal
	var opErr error
	s.loop.Do(func() {
		var exists bool
		model, exists, opErr = s.store.Get(id)
		if opErr != nil {
			return
		}
		if !exists {
			refusal = &operationRefusal{status: http.StatusNotFound, message: notFound}
			return
		}
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if !access.Exists || !access.CanView {
			refusal = &operationRefusal{status: http.StatusNotFound, message: notFound}
		}
	})
	return model, refusal, opErr
}

type operationRefusal struct {
	status  int
	message string
}

func writeRefusal(access ApplicationAccess, creating bool) *operationRefusal {
	if !access.Exists {
		if creating {
			return &operationRefusal{status: http.StatusBadRequest, message: "unknown application id"}
		}
		return &operationRefusal{status: http.StatusNotFound, message: notFound}
	}
	if !access.CanView {
		return &operationRefusal{status: http.StatusNotFound, message: notFound}
	}
	if access.Protected {
		return &operationRefusal{status: http.StatusForbidden, message: "protected application cannot be modified"}
	}
	if !access.CanEdit {
		return &operationRefusal{status: http.StatusForbidden, message: "insufficient access to this application"}
	}
	return nil
}

func writeReadOutcome(w http.ResponseWriter, refusal *operationRefusal, err error) bool {
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return true
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read information model: "+err.Error())
		return true
	}
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBytes+1))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	if int64(len(body)) > maxJSONBytes {
		httpapi.Error(w, http.StatusRequestEntityTooLarge, errBodyTooLarge.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func requestActor(r *http.Request) string {
	if principal := httpapi.PrincipalFrom(r.Context()); principal != nil {
		return principal.Username
	}
	return ""
}

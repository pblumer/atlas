package panorama

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

const maxJSONBytes = 2*MaxXMLBytes + 64<<10

var errBodyTooLarge = errors.New("request body is too large")

// ApplicationAccess is the caller's resolved access to the Atlas process
// application that owns a Panorama model. The API server supplies this result
// from its existing project/application scopes; Panorama does not maintain a
// second ACL model.
type ApplicationAccess struct {
	Exists    bool
	CanView   bool
	CanEdit   bool
	Protected bool
}

// AccessResolver resolves application ownership on the API run loop. A resolver
// may read the server's application store because Service invokes it only from a
// loop turn; it must not call Loop.Do recursively.
type AccessResolver func(r *http.Request, applicationID string) (ApplicationAccess, error)

// IDGenerator mints an opaque Panorama model id off the run loop.
type IDGenerator func() (string, error)

// Clock supplies deterministic timestamps in tests.
type Clock func() time.Time

// Service serves the Panorama model-library HTTP area (ADR-0189). Its only
// mutable dependency is Store, and every read and write of that store goes
// through loop, preserving the design-time single-writer boundary.
type Service struct {
	loop   *runloop.Loop
	store  *Store
	access AccessResolver
	newID  IDGenerator
	now    Clock
}

// CountForApplicationOnLoop counts models owned by one application. It is a
// composition-root hook for application summaries and deletion guards; callers
// must already be executing on the Service's run loop, as the name makes
// explicit, so it deliberately does not call Loop.Do recursively.
func (s *Service) CountForApplicationOnLoop(applicationID string) (int, error) {
	models, err := s.store.ForApplication(applicationID)
	if err != nil {
		return 0, err
	}
	return len(models), nil
}

// CountsByApplicationOnLoop returns the artifact-count contribution Panorama
// makes to every application. It has the same run-loop precondition as
// CountForApplicationOnLoop and performs one store scan for a whole listing.
func (s *Service) CountsByApplicationOnLoop() (map[string]int, error) {
	models, err := s.store.LoadAll()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, model := range models {
		counts[model.ApplicationID]++
	}
	return counts, nil
}

// New builds a Panorama service over a run-loop-owned store.
func New(loop *runloop.Loop, store *Store, access AccessResolver, newID IDGenerator, now Clock) *Service {
	return &Service{loop: loop, store: store, access: access, newID: newID, now: now}
}

type createRequest struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Notation      string `json:"notation"`
	XML           string `json:"xml"`
}

// HandleCreate imports an Open Exchange document into an application-owned
// Panorama model. The XML is validated but stored byte-for-byte.
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
	if payload.Notation == "" {
		payload.Notation = NotationArchiMate32
	}
	if payload.Notation != NotationArchiMate32 {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("unsupported notation %q", payload.Notation))
		return
	}
	validation := Validate([]byte(payload.XML))
	if !validation.Valid {
		writeValidationFailure(w, validation)
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = validation.Name
	}

	id, err := s.newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "mint Panorama model id: "+err.Error())
		return
	}
	now := s.now().Unix()
	actor := requestActor(r)
	model := Model{
		ID: id, ApplicationID: payload.ApplicationID, Name: name,
		Notation: NotationArchiMate32, Revision: 1, XML: payload.XML,
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
		httpapi.Error(w, http.StatusInternalServerError, "save Panorama model: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusCreated, summarize(model))
}

// HandleList lists metadata for every model the caller may view. XML is loaded
// from disk as part of the sidecar record but is never copied into the response.
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
		for _, model := range models {
			if applicationID != "" && model.ApplicationID != applicationID {
				continue
			}
			access, err := s.access(r, model.ApplicationID)
			if err != nil {
				opErr = err
				return
			}
			if access.Exists && access.CanView {
				out = append(out, summarize(model))
			}
		}
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list Panorama models: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// HandleGet returns one model's metadata without its XML.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	httpapi.JSON(w, http.StatusOK, summarize(model))
}

// HandleXML exports the canonical Open Exchange document unchanged.
func (s *Service) HandleXML(w http.ResponseWriter, r *http.Request) {
	model, refusal, err := s.readModel(r, r.PathValue("id"))
	if writeReadOutcome(w, refusal, err) {
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", model.ID+".archimate.xml"))
	w.Header().Set("X-Panorama-Revision", strconv.FormatInt(model.Revision, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, model.XML)
}

type updateRequest struct {
	ExpectedRevision int64   `json:"expectedRevision"`
	Name             *string `json:"name"`
	XML              *string `json:"xml"`
}

// HandleUpdate saves a new revision when expectedRevision still matches. The
// check and atomic save happen in one run-loop turn, so two browser sessions
// cannot both overwrite the same revision successfully.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	var payload updateRequest
	if !decodeJSON(w, r, &payload) {
		return
	}
	if payload.ExpectedRevision < 1 {
		httpapi.Error(w, http.StatusBadRequest, "expectedRevision must be at least 1")
		return
	}
	if payload.Name == nil && payload.XML == nil {
		httpapi.Error(w, http.StatusBadRequest, "name or xml is required")
		return
	}
	var name string
	if payload.Name != nil {
		name = strings.TrimSpace(*payload.Name)
		if name == "" {
			httpapi.Error(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
	}
	if payload.XML != nil {
		validation := Validate([]byte(*payload.XML))
		if !validation.Valid {
			writeValidationFailure(w, validation)
			return
		}
	}

	id := r.PathValue("id")
	now := s.now().Unix()
	actor := requestActor(r)
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
			refusal = &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
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
		if model.Revision != payload.ExpectedRevision {
			refusal = &operationRefusal{
				status: http.StatusConflict,
				message: fmt.Sprintf("revision conflict: expected %d, current revision is %d",
					payload.ExpectedRevision, model.Revision),
			}
			return
		}
		if payload.Name != nil {
			model.Name = name
		}
		if payload.XML != nil {
			model.XML = *payload.XML
		}
		model.Revision++
		model.UpdatedAt = now
		model.UpdatedBy = actor
		opErr = s.store.Save(model)
	})
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "update Panorama model: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, summarize(model))
}

// HandleDelete removes a model. A missing or inaccessible model is a 404 so an
// application scope the caller cannot see is not disclosed.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var refusal *operationRefusal
	var opErr error
	s.loop.Do(func() {
		model, exists, err := s.store.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !exists {
			refusal = &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
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
		opErr = s.store.Delete(id)
	})
	if refusal != nil {
		httpapi.Error(w, refusal.status, refusal.message)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete Panorama model: "+opErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleValidate checks an Open Exchange XML document without storing it.
func (s *Service) HandleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := readBounded(r.Body, MaxXMLBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		httpapi.Error(w, status, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, Validate(body))
}

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
			refusal = &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
			return
		}
		access, err := s.access(r, model.ApplicationID)
		if err != nil {
			opErr = err
			return
		}
		if !access.Exists || !access.CanView {
			refusal = &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
			return
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
		return &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
	}
	if !access.CanView {
		return &operationRefusal{status: http.StatusNotFound, message: "no such Panorama model"}
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
		httpapi.Error(w, http.StatusInternalServerError, "read Panorama model: "+err.Error())
		return true
	}
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := readBounded(r.Body, maxJSONBytes)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		httpapi.Error(w, status, err.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, nil
}

func writeValidationFailure(w http.ResponseWriter, result ValidationResult) {
	httpapi.JSON(w, http.StatusBadRequest, map[string]any{
		"error":      "invalid ArchiMate Open Exchange model",
		"validation": result,
	})
}

func requestActor(r *http.Request) string {
	if principal := httpapi.PrincipalFrom(r.Context()); principal != nil {
		return principal.Username
	}
	return ""
}

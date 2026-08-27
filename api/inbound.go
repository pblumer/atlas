package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"

	"github.com/pblumer/atlas/api/httpapi"
)

// feelExpr normalizes a correlation-key input to a bare FEEL expression: it trims
// whitespace and strips a single leading '=' (the Zeebe authoring convention for
// "this is an expression"), so both "= orderId" and "orderId" are accepted and
// stored uniformly as the compilable form.
func feelExpr(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "=")
	return strings.TrimSpace(s)
}

// handleListInboundSubscriptions lists the inbound event subscriptions of one clio
// connector, oldest first.
func (s *Server) handleListInboundSubscriptions(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	// A subscription list is every message name this connector publishes under, and
	// a message name is the whole key an inbound event is delivered by. Reading it is
	// therefore reading the connector's configuration, and needs viewer (ADR-0205).
	if _, code, msg := s.authorizeConnector(r, connID, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var (
		out     []inboundSubscription
		loadErr error
	)
	s.do(func() {
		var all []inboundSubscription
		if all, loadErr = s.inboundSubs.LoadAll(); loadErr != nil {
			return
		}
		for _, sub := range all {
			if sub.ConnectorID == connID {
				out = append(out, sub)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list subscriptions: "+loadErr.Error())
		return
	}
	if out == nil {
		out = []inboundSubscription{}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleCreateInboundSubscription creates an inbound subscription for a clio
// connector (ADR-0075). It validates that the connector exists and is a clio
// connector, that a message name and watched subject are given, and that the
// correlation key (if any) compiles — a bad FEEL expression is rejected at config
// time, not left to fail on every poll.
func (s *Server) handleCreateInboundSubscription(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	// Pointing a connector at a message name is the act this whole measure exists
	// for: it decides which processes its events start. Editor on the connector
	// (ADR-0205).
	if _, code, msg := s.authorizeConnector(r, connID, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		WatchedSubject string `json:"watchedSubject"`
		Recursive      bool   `json:"recursive"`
		MessageName    string `json:"messageName"`
		CorrelationKey string `json:"correlationKey"`
		Enabled        *bool  `json:"enabled"`
		StartFromTip   *bool  `json:"startFromTip"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	subject := strings.TrimSpace(p.WatchedSubject)
	messageName := strings.TrimSpace(p.MessageName)
	corr := feelExpr(p.CorrelationKey)
	if messageName == "" || subject == "" {
		httpapi.Error(w, http.StatusBadRequest, "messageName and watchedSubject are required")
		return
	}
	if corr != "" {
		if _, err := expr.CompileAuto(corr); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "correlationKey is not a valid FEEL expression: "+err.Error())
			return
		}
	}
	id, err := newID()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	// Forward-only by default: a new watch skips the subject's backlog rather than
	// replaying it into one process per historical event (ADR-0075). An operator who
	// wants to backfill the whole history sets startFromTip:false explicitly.
	startFromTip := true
	if p.StartFromTip != nil {
		startFromTip = *p.StartFromTip
	}
	rec := inboundSubscription{
		ID: id, ConnectorID: connID, WatchedSubject: subject, Recursive: p.Recursive,
		MessageName: messageName, CorrelationKey: corr, Enabled: enabled,
		StartFromTip: startFromTip, CreatedAt: time.Now().Unix(),
	}
	var (
		notClio bool
		saveErr error
	)
	s.do(func() {
		conn, ok, e := s.connectors.Get(connID)
		if e != nil {
			saveErr = e
			return
		}
		if !ok || conn.Kind != connectorKindClio {
			notClio = true
			return
		}
		saveErr = s.inboundSubs.Save(rec)
	})
	switch {
	case notClio:
		httpapi.Error(w, http.StatusBadRequest, "no clio connector with that id")
		return
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save subscription: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleUpdateInboundSubscription applies a partial change to a subscription (its
// subject, message name, correlation key, recursion, or enabled state). It
// re-validates the correlation key when one is supplied.
func (s *Server) handleUpdateInboundSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		WatchedSubject *string `json:"watchedSubject"`
		Recursive      *bool   `json:"recursive"`
		MessageName    *string `json:"messageName"`
		CorrelationKey *string `json:"correlationKey"`
		Enabled        *bool   `json:"enabled"`
		StartFromTip   *bool   `json:"startFromTip"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	// Authorized after the body is parsed, which is the order the project handlers
	// use (ADR-0071) and the one that leaks nothing: a malformed body is answered the
	// same way for everyone, and every well-formed request past this point is
	// answered by access alone.
	if code, msg := s.authorizeSubscription(r, id, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if p.CorrelationKey != nil && feelExpr(*p.CorrelationKey) != "" {
		if _, err := expr.CompileAuto(feelExpr(*p.CorrelationKey)); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "correlationKey is not a valid FEEL expression: "+err.Error())
			return
		}
	}
	var (
		rec     inboundSubscription
		found   bool
		saveErr error
	)
	s.do(func() {
		var e error
		rec, found, e = s.inboundSubs.Get(id)
		if e != nil {
			saveErr = e
			return
		}
		if !found {
			return
		}
		if p.WatchedSubject != nil {
			rec.WatchedSubject = strings.TrimSpace(*p.WatchedSubject)
		}
		if p.Recursive != nil {
			rec.Recursive = *p.Recursive
		}
		if p.MessageName != nil {
			rec.MessageName = strings.TrimSpace(*p.MessageName)
		}
		if p.CorrelationKey != nil {
			rec.CorrelationKey = feelExpr(*p.CorrelationKey)
		}
		if p.Enabled != nil {
			rec.Enabled = *p.Enabled
		}
		if p.StartFromTip != nil {
			rec.StartFromTip = *p.StartFromTip
		}
		saveErr = s.inboundSubs.Save(rec)
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "update subscription: "+saveErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no subscription with that id")
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleDeleteInboundSubscription removes a subscription so the bridge stops polling
// it.
func (s *Server) handleDeleteInboundSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if code, msg := s.authorizeSubscription(r, id, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var delErr error
	s.do(func() { delErr = s.inboundSubs.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete subscription: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorizeSubscription authorizes a request against the connector a subscription
// belongs to (ADR-0205). A subscription is governed by its connector's scope, the
// way an artifact is governed by its project's — it has no sharing of its own,
// because "who may point this mailbox somewhere" is a fact about the mailbox.
//
// A subscription that does not exist passes: there is no connector to authorize
// against and nothing to protect, and the handler behind this already has its own
// answer for a missing record — its own 404, or an idempotent delete. Refusing here
// would replace those with an answer about access, which is the wrong thing to tell
// somebody whose id is a typo.
func (s *Server) authorizeSubscription(r *http.Request, subID, minRole string) (int, string) {
	var (
		sub    inboundSubscription
		found  bool
		getErr error
	)
	s.do(func() { sub, found, getErr = s.inboundSubs.Get(subID) })
	if getErr != nil {
		return http.StatusInternalServerError, "read subscription: " + getErr.Error()
	}
	if !found {
		return 0, ""
	}
	_, code, msg := s.authorizeConnector(r, sub.ConnectorID, minRole)
	return code, msg
}

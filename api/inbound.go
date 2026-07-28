package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"
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
	var (
		out     []inboundSubscription
		loadErr error
	)
	s.do(func() {
		var all []inboundSubscription
		if all, loadErr = s.inboundSubs.loadAll(); loadErr != nil {
			return
		}
		for _, sub := range all {
			if sub.ConnectorID == connID {
				out = append(out, sub)
			}
		}
	})
	if loadErr != nil {
		writeError(w, http.StatusInternalServerError, "list subscriptions: "+loadErr.Error())
		return
	}
	if out == nil {
		out = []inboundSubscription{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateInboundSubscription creates an inbound subscription for a clio
// connector (ADR-0074). It validates that the connector exists and is a clio
// connector, that a message name and watched subject are given, and that the
// correlation key (if any) compiles — a bad FEEL expression is rejected at config
// time, not left to fail on every poll.
func (s *Server) handleCreateInboundSubscription(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		WatchedSubject string `json:"watchedSubject"`
		Recursive      bool   `json:"recursive"`
		MessageName    string `json:"messageName"`
		CorrelationKey string `json:"correlationKey"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	subject := strings.TrimSpace(p.WatchedSubject)
	messageName := strings.TrimSpace(p.MessageName)
	corr := feelExpr(p.CorrelationKey)
	if messageName == "" || subject == "" {
		writeError(w, http.StatusBadRequest, "messageName and watchedSubject are required")
		return
	}
	if corr != "" {
		if _, err := expr.CompileAuto(corr); err != nil {
			writeError(w, http.StatusBadRequest, "correlationKey is not a valid FEEL expression: "+err.Error())
			return
		}
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate id: "+err.Error())
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	rec := inboundSubscription{
		ID: id, ConnectorID: connID, WatchedSubject: subject, Recursive: p.Recursive,
		MessageName: messageName, CorrelationKey: corr, Enabled: enabled,
		CreatedAt: time.Now().Unix(),
	}
	var (
		notClio bool
		saveErr error
	)
	s.do(func() {
		conn, ok, e := s.connectors.get(connID)
		if e != nil {
			saveErr = e
			return
		}
		if !ok || conn.Kind != connectorKindClio {
			notClio = true
			return
		}
		saveErr = s.inboundSubs.save(rec)
	})
	switch {
	case notClio:
		writeError(w, http.StatusBadRequest, "no clio connector with that id")
		return
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "save subscription: "+saveErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUpdateInboundSubscription applies a partial change to a subscription (its
// subject, message name, correlation key, recursion, or enabled state). It
// re-validates the correlation key when one is supplied.
func (s *Server) handleUpdateInboundSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var p struct {
		WatchedSubject *string `json:"watchedSubject"`
		Recursive      *bool   `json:"recursive"`
		MessageName    *string `json:"messageName"`
		CorrelationKey *string `json:"correlationKey"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.CorrelationKey != nil && feelExpr(*p.CorrelationKey) != "" {
		if _, err := expr.CompileAuto(feelExpr(*p.CorrelationKey)); err != nil {
			writeError(w, http.StatusBadRequest, "correlationKey is not a valid FEEL expression: "+err.Error())
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
		rec, found, e = s.inboundSubs.get(id)
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
		saveErr = s.inboundSubs.save(rec)
	})
	switch {
	case saveErr != nil:
		writeError(w, http.StatusInternalServerError, "update subscription: "+saveErr.Error())
		return
	case !found:
		writeError(w, http.StatusNotFound, "no subscription with that id")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleDeleteInboundSubscription removes a subscription so the bridge stops polling
// it.
func (s *Server) handleDeleteInboundSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var delErr error
	s.do(func() { delErr = s.inboundSubs.delete(id) })
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "delete subscription: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

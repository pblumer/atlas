package authz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Document is a policy as an administrator writes it. It is decoded strictly: a field
// this build does not know is refused rather than ignored, because an ignored matcher
// ("subjectGroupAny") is a rule that matches everyone.
type Document struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	// Priority orders policies, lowest first. It decides nothing under the fixed
	// deny-overrides combination; it decides which policy is reported as deciding
	// and the order of the explanation.
	Priority      int       `json:"priority,omitempty"`
	Target        Target    `json:"target,omitempty"`
	RuleCombining string    `json:"ruleCombining"`
	Rules         []RuleDoc `json:"rules"`
	// DMN is a DMN model whose decisions rules may name. It is compiled once, at
	// publication.
	DMN string `json:"dmn,omitempty"`
}

// Target selects the requests a policy speaks to. Every list that is set must contain
// the request's value; an empty target applies to every governed action.
type Target struct {
	WorkerTypes   []string `json:"workerTypes,omitempty"`
	Workers       []string `json:"workers,omitempty"`
	Operations    []string `json:"operations,omitempty"`
	IdentityModes []string `json:"identityModes,omitempty"`
}

// RuleDoc is one rule: a static effect under conditions, or a DMN decision.
type RuleDoc struct {
	ID string `json:"id"`
	// Effect is "Permit" or "Deny". Exactly one of Effect and Decision is set.
	Effect string `json:"effect,omitempty"`
	// Decision names a decision of the policy's DMN model; its outputs "effect" and
	// "reasonCode" are the rule's answer.
	Decision   string `json:"decision,omitempty"`
	ReasonCode string `json:"reasonCode,omitempty"`
	When       Match  `json:"when,omitempty"`
	// Condition is a FEEL boolean over subject, actor, action, resource and context.
	Condition   string          `json:"condition,omitempty"`
	Obligations []ObligationDoc `json:"obligations,omitempty"`
}

// Match is a rule's structured matchers. Every set list must match; a list matches
// when it contains the request's value (or, for SubjectRolesAny and
// SubjectGroupsAny, any of the subject's).
type Match struct {
	SubjectRolesAny  []string `json:"subjectRolesAny,omitempty"`
	SubjectGroupsAny []string `json:"subjectGroupsAny,omitempty"`
	SubjectIDs       []string `json:"subjectIds,omitempty"`
	Operations       []string `json:"operations,omitempty"`
	Workers          []string `json:"workers,omitempty"`
	IdentityModes    []string `json:"identityModes,omitempty"`
	ProcessIDs       []string `json:"processIds,omitempty"`
	// ResourcePrefixes match when the canonical resource id starts with one.
	ResourcePrefixes []string `json:"resourcePrefixes,omitempty"`
}

// Obligation types.
const (
	ObligationApproval            = "approval"
	ObligationFreshAuthentication = "fresh-authentication"
)

// ObligationDoc is a condition a Permit carries that the server verifies from its own
// records before the action runs.
type ObligationDoc struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// approval
	ApproverGroup    string `json:"approverGroup,omitempty"`
	Approvals        int    `json:"approvals,omitempty"`
	ValidForSeconds  int64  `json:"validForSeconds,omitempty"`
	ExcludeRequester bool   `json:"excludeRequester,omitempty"`
	// fresh-authentication
	MaxAgeSeconds int64 `json:"maxAgeSeconds,omitempty"`
}

// ParseDocument decodes one policy, refusing unknown fields and trailing content.
func ParseDocument(src []byte) (Document, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("policy: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Document{}, fmt.Errorf("policy: content after the policy document")
	}
	return doc, nil
}

package sharepointserver

import (
	"errors"
	"slices"

	"github.com/pblumer/atlas/job"
)

// The codes of the refusals a process may have a path for. Each is a BPMN error when
// the task's process catches it and an ordinary failure otherwise, which keeps the
// retries a transient fault is owed (ADR-0461's rule). None changes on a retry. The
// bootstrap sets them from a check it made itself, never by reading an error's text,
// which SharePoint writes in the farm's language.
const (
	// CodeSiteExists: a site already lives at that address.
	CodeSiteExists = "SHAREPOINT_SERVER_SITE_EXISTS"
	// CodeSiteNotFound: no site lives at that address.
	CodeSiteNotFound = "SHAREPOINT_SERVER_SITE_NOT_FOUND"
	// CodeDeletedSiteNotFound: the recycle bin holds no deleted site for that address,
	// because it was never deleted or its retention has passed.
	CodeDeletedSiteNotFound = "SHAREPOINT_SERVER_DELETED_SITE_NOT_FOUND"
	// CodeAccountNotFound: the directory does not know a named account or AD group.
	CodeAccountNotFound = "SHAREPOINT_SERVER_ACCOUNT_NOT_FOUND"
	// CodeGroupMissing: the site has no such default group, typically because it was not
	// made by this Worker.
	CodeGroupMissing = "SHAREPOINT_SERVER_GROUP_MISSING"
	// CodeOwnerRequired: the account to remove is the site's primary or secondary owner;
	// set another owner first.
	CodeOwnerRequired = "SHAREPOINT_SERVER_OWNER_REQUIRED"
	// CodeQuotaRefused: the site uses a quota template, which excludes an individual
	// quota; a person changes the template.
	CodeQuotaRefused = "SHAREPOINT_SERVER_QUOTA_REFUSED"
)

// Codes lists every code a refusal carries, in the order the handbook explains them.
var Codes = []string{CodeSiteExists, CodeSiteNotFound, CodeDeletedSiteNotFound, CodeAccountNotFound, CodeGroupMissing, CodeOwnerRequired, CodeQuotaRefused}

// Refusal is an operation's refusal under one of [Codes].
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// thrown is err as the job ends with it: a refusal whose code the task's process
// catches becomes the BPMN error the process has a path for; anything else stays a
// failure and keeps its retries (ADR-0461).
func thrown(codes []string, err error) error {
	var r *Refusal
	if errors.As(err, &r) && slices.Contains(codes, r.Code) {
		return &job.BPMNError{Code: r.Code, Message: r.Message}
	}
	return err
}

package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/brandimage"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// A picture of the person behind an account (ADR-0368).
//
// Atlas showed people as strings. An approval said `usr_4be5b4ad`, the portal's
// corner drew an empty circle, and a recipient picked out of the directory was a
// name in a list of names. That is fine while a person works with three
// colleagues and stops being fine in an estate: the whole reason a face is worth
// anything on a screen is that reading one is faster and less error-prone than
// reading a name, which matters most where the mistake is expensive — ordering in
// somebody else's name, deciding somebody else's request.
//
// # Where it lives
//
// Beside the account's own record, in the users directory, under the same
// key-encoded name the record uses. Two consequences follow and both are wanted:
// a snapshot that carries the accounts carries their pictures without anybody
// adding a path to an allowlist, and deleting an account deletes its picture —
// enforced in the store rather than in a handler, so every deletion path does it.
//
// # Where it comes from
//
// Two provenances, one of them built here. A person uploads their own, or an
// administrator uploads it for them. The other is the directory: a tenant that
// already holds a photo for everybody should not be asked to collect them twice.
// That path arrives through the mirror the directory already uses — Atlas holds no
// tenant credential and must not start (ADR-0332) — and until it lands, no picture
// carries [AvatarFromDirectory]. The constant exists now so the field means the
// same thing before and after, rather than being widened later under records that
// predate it.

// Where a picture came from. It is recorded on the account rather than derived
// from the bytes, because nothing in a JPEG says who chose it, and the difference
// is exactly what somebody looking at a wrong picture needs to know: whether to
// change it here or in the directory.
const (
	// AvatarUploaded marks a picture a person put there — their own, or an
	// administrator's on their behalf. A directory mirror will not overwrite one.
	AvatarUploaded = "uploaded"

	// AvatarFromDirectory marks one the directory supplied. Nothing produces it
	// yet; see the note above.
	AvatarFromDirectory = "entra"
)

// avatarPath is where one account's picture is stored: the record's own file
// name with its extension replaced.
//
// Derived from [sidecar.Store.FileFor] rather than built from the id, and that is
// the whole of the path safety. The id reaching here came off a URL; encoding it
// the way the store encodes its own keys means a picture can no more escape the
// directory than a record can, and there is no second encoding to keep in step.
func (s *userStore) avatarPath(id, ext string) string {
	return strings.TrimSuffix(s.FileFor(id), ".json") + ".avatar." + ext
}

// avatar returns an account's picture and the type to serve it as, and whether
// there is one. A missing picture is the ordinary state, not an error.
func (s *userStore) avatar(id string) (data []byte, contentType string, ok bool, err error) {
	for _, ext := range brandimage.Photo.Exts() {
		b, readErr := os.ReadFile(s.avatarPath(id, ext))
		if readErr == nil {
			ct, _ := brandimage.Photo.TypeFor(ext)
			return b, ct, true, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, "", false, fmt.Errorf("userstore: read avatar: %w", readErr)
		}
	}
	return nil, "", false, nil
}

// saveAvatar writes the picture durably and drops any other format left from a
// previous upload, so a switch from PNG to JPEG never leaves a stale file the
// read above would find first.
func (s *userStore) saveAvatar(id string, data []byte, contentType string) error {
	ext, ok := brandimage.Photo.ExtFor(contentType)
	if !ok {
		return fmt.Errorf("userstore: unsupported picture type %q", contentType)
	}
	if err := sidecar.WriteFile(s.Dir(), s.avatarPath(id, ext), data); err != nil {
		return err
	}
	for _, other := range brandimage.Photo.Exts() {
		if other == ext {
			continue
		}
		if err := os.Remove(s.avatarPath(id, other)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("userstore: remove stale avatar: %w", err)
		}
	}
	return sidecar.FsyncDir(s.Dir())
}

// clearAvatar removes an account's picture in every format. Idempotent: a person
// who never had one and one who just removed theirs are the same state.
func (s *userStore) clearAvatar(id string) error {
	for _, ext := range brandimage.Photo.Exts() {
		if err := os.Remove(s.avatarPath(id, ext)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("userstore: remove avatar: %w", err)
		}
	}
	return sidecar.FsyncDir(s.Dir())
}

// Delete removes the account and the picture beside it.
//
// It overrides the embedded store's Delete rather than sitting beside it, because
// the alternative is a rule every future deletion path has to remember. A picture
// outliving its account is not merely untidy: the ids are assigned, so the next
// account to be handed that id would inherit a stranger's face.
func (s *userStore) Delete(id string) error {
	if err := s.clearAvatar(id); err != nil {
		return err
	}
	return s.Store.Delete(id)
}

// mayChangeAvatar reports whether this caller may set or remove the picture on
// the named account.
//
// Yourself, or an administrator. Not an operator: an operator runs what is
// deployed, and changing how a colleague appears to everybody else is not running
// anything. With enforcement off there is nobody to be, exactly as every other
// gate in the product has it.
func (s *Server) mayChangeAvatar(r *http.Request, id string) bool {
	if !s.authEnabled {
		return true
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		return false
	}
	return p.UserID == id || p.HasRole(RoleAdmin)
}

// handleGetAvatar serves an account's picture, or 404 when it has none.
//
// Readable by anybody signed in, which is the point of having one: a face is
// worth something beside a name in a task list, an approval and a recipient
// picker, and those are read by colleagues rather than by administrators. It
// discloses no more than the account listing the same caller can already read.
func (s *Server) handleGetAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		data []byte
		ct   string
		ok   bool
		err  error
	)
	s.do(func() { data, ct, ok, err = s.users.avatar(id) })
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read picture: "+err.Error())
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "this account has no picture")
	default:
		brandimage.Serve(w, ct, data)
	}
}

// handleSetAvatar stores an account's picture from the raw request body.
//
// The body is read before the run loop is entered. Reading a request body is
// waiting on a network, and the single writer must not wait on anybody
// (invariant I3).
func (s *Server) handleSetAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ct := brandimage.NormalizeType(r.Header.Get("Content-Type"))
	if _, ok := brandimage.Photo.ExtFor(ct); !ok {
		httpapi.Error(w, http.StatusUnsupportedMediaType,
			"a picture is uploaded as image/png or image/jpeg")
		return
	}
	max := s.budgets().Asset
	// One byte past the budget, so an over-large picture is refused rather than
	// truncated into a smaller one that still passes the format check.
	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	case len(data) == 0:
		httpapi.Error(w, http.StatusBadRequest, "empty picture body")
		return
	case int64(len(data)) > max:
		httpapi.Error(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the picture exceeds the %d byte limit", max))
		return
	case !brandimage.Valid(ct, data):
		httpapi.Error(w, http.StatusBadRequest, "the body is not a valid "+ct+" image")
		return
	}
	// Asked before the store is touched, and separately from whether the account
	// exists: "not yours" and "no such account" are different sentences, and a
	// caller who may not act on somebody else's account learns nothing from being
	// told which ids exist.
	if !s.mayChangeAvatar(r, id) {
		httpapi.Error(w, http.StatusForbidden, "a picture is set by the account itself or by an administrator")
		return
	}
	var (
		found bool
		opErr error
	)
	s.do(func() {
		u, ok, e := s.users.Get(id)
		if e != nil || !ok {
			opErr = e
			return
		}
		found = true
		if opErr = s.users.saveAvatar(id, data, ct); opErr != nil {
			return
		}
		// The provenance travels on the record, so a reader of the account knows
		// where to go to change what they are looking at.
		u.AvatarSource = AvatarUploaded
		u.UpdatedAt = time.Now().Unix()
		opErr = s.users.Save(u)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save picture: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteAvatar removes an account's picture. Same gate as setting one:
// taking a face away changes how somebody appears as much as putting one there.
func (s *Server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.mayChangeAvatar(r, id) {
		httpapi.Error(w, http.StatusForbidden, "a picture is removed by the account itself or by an administrator")
		return
	}
	var (
		found bool
		opErr error
	)
	s.do(func() {
		u, ok, e := s.users.Get(id)
		if e != nil || !ok {
			opErr = e
			return
		}
		found = true
		if opErr = s.users.clearAvatar(id); opErr != nil {
			return
		}
		u.AvatarSource = ""
		u.UpdatedAt = time.Now().Unix()
		opErr = s.users.Save(u)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "remove picture: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no user with that id")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

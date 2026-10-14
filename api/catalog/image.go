package catalog

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/brandimage"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// A product's own picture.
//
// A catalogue row is a name and a price, and a person choosing between two phones
// is choosing between two names. The picture is what a shop has that a list does
// not, and it is the one thing about a product that no amount of text replaces.
//
// It is stored the way the catalogue's brand mark is (ADR-0316) and for the same
// reasons: bytes are not a record. Up to half a megabyte of opaque data in the item
// record would ride along in every product listing, in every release, and in the
// answer to every question nobody asked with it. So the picture is a file beside the
// stores, named by the item it belongs to, and there is **no flag in the record
// saying one exists** — the file is the fact, and a second copy of that fact is a
// second copy to be wrong after a restore that brought the JSON and not the image.
//
// # What a release does not freeze
//
// A release freezes what was promised: the product, its variants, the approval rule,
// the ceiling, the price. It does not freeze the picture, and that is deliberate. A
// picture is how a thing is shown and not what was agreed — a better photograph of
// the same laptop is not a different laptop, and an order placed last month showing
// last month's photograph would be a record nobody asked for. An order that must
// survive the product being withdrawn already survives it: it carries the texts and
// the price it was placed under, and the picture is simply absent when the product
// is gone.

// picturePath is where one item's picture is stored. The id is hex-encoded, the
// scheme sidecar uses for exactly this reason: a request-supplied key that reaches a
// filename is a path, and hex is the encoding under which "../secret" is a file name
// and not a direction.
func (s *Store) picturePath(itemID, ext string) string {
	return filepath.Join(s.pictures, hex.EncodeToString([]byte(itemID))+"."+ext)
}

// Picture reads an item's picture and the type to serve it as. An item without one
// is not an error — it is the ordinary case, and the caller falls back.
func (s *Store) Picture(itemID string) (data []byte, contentType string, ok bool, err error) {
	for _, ext := range brandimage.Picture.Exts() {
		b, readErr := os.ReadFile(s.picturePath(itemID, ext))
		if readErr == nil {
			ct, _ := brandimage.Picture.TypeFor(ext)
			return b, ct, true, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, "", false, fmt.Errorf("catalogstore: read picture: %w", readErr)
		}
	}
	return nil, "", false, nil
}

// SavePicture writes an item's picture durably, then removes any other format it
// had, so replacing a PNG with a JPEG cannot leave a stale file that [Picture]
// would serve instead.
func (s *Store) SavePicture(itemID string, data []byte, contentType string) error {
	ext, ok := brandimage.Picture.ExtFor(contentType)
	if !ok {
		return fmt.Errorf("catalogstore: unsupported picture type %q", contentType)
	}
	if err := sidecar.WriteFile(s.pictures, s.picturePath(itemID, ext), data); err != nil {
		return err
	}
	for _, other := range brandimage.Picture.Exts() {
		if other == ext {
			continue
		}
		if err := os.Remove(s.picturePath(itemID, other)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalogstore: remove stale picture: %w", err)
		}
	}
	return sidecar.FsyncDir(s.pictures)
}

// ClearPicture removes an item's picture, whatever format it was in. An item that
// had none is not an error: the caller asked for a state, and it holds.
func (s *Store) ClearPicture(itemID string) error {
	for _, ext := range brandimage.Picture.Exts() {
		if err := os.Remove(s.picturePath(itemID, ext)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalogstore: remove picture: %w", err)
		}
	}
	return sidecar.FsyncDir(s.pictures)
}

// maySeePicture reports whether this principal may see a product's picture at all.
//
// Not "may read its home catalogue", which is the gate on maintaining it: a product
// is referenced by catalogues rather than owned by one (ADR-0315), so a customer of
// catalogue B legitimately orders a product whose home is catalogue A, and a gate on
// the home alone would show that customer a name and no picture — the one row on the
// page that looks broken.
//
// So the question is the one the portal actually asks: does any catalogue this
// person may read offer this product. Runs on the loop; callers hold it.
func (s *Service) maySeePicture(itemID, home string, p *httpapi.Principal) bool {
	if c, ok, err := s.store.Catalog(home); err == nil && ok && s.mayRead(c, p) {
		return true
	}
	cats, err := s.store.Catalogs()
	if err != nil {
		return false
	}
	for _, c := range cats {
		for _, id := range c.Items {
			if id == itemID && s.mayRead(c, p) {
				return true
			}
		}
	}
	return false
}

// HandleGetPicture serves a product's picture, or 404 when it has none.
//
// A product nobody may see and a product with no picture are the same 404, on
// purpose: the two have to be indistinguishable, or the status code answers the
// question the gate withholds.
func (s *Service) HandleGetPicture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		data  []byte
		ct    string
		found bool
		has   bool
		opErr error
	)
	s.loop.Do(func() {
		var it Item
		if it, found, opErr = s.store.Item(id); opErr != nil || !found {
			return
		}
		if found = s.maySeePicture(it.ID, it.HomeCatalog, p); !found {
			return
		}
		data, ct, has, opErr = s.store.Picture(id)
	})

	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read picture: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no product "+id)
	case !has:
		httpapi.Error(w, http.StatusNotFound, "product "+id+" has no picture")
	default:
		brandimage.Serve(w, ct, data)
	}
}

// HandleSetPicture stores a product's picture from the raw request body.
//
// The gate is the one that changes the product: whoever maintains its home
// catalogue. A picture is part of how the product is offered, and somebody who may
// not rename it may not re-illustrate it either.
//
// The body is read before the loop is entered. Reading a request body is waiting on
// a network, and the single writer must not wait on anybody (invariant I3).
func (s *Service) HandleSetPicture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	ct := brandimage.NormalizeType(r.Header.Get("Content-Type"))
	if _, ok := brandimage.Picture.ExtFor(ct); !ok {
		httpapi.Error(w, http.StatusUnsupportedMediaType,
			"a product picture is uploaded as image/png, image/jpeg or image/svg+xml")
		return
	}
	// One byte past the budget, so an over-large image is refused rather than
	// truncated into a smaller one that still passes the format check.
	max := s.budgets().Asset
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

	found, allowed, opErr := s.withItemHome(id, p, func(it Item) error {
		return s.store.SavePicture(it.ID, data, ct)
	})
	s.answerPictureWrite(w, id, found, allowed, opErr, "save picture: ")
}

// HandleDeletePicture removes a product's picture. Same gate as setting one: taking
// the picture away changes how the product is offered as much as putting one there.
func (s *Service) HandleDeletePicture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	found, allowed, opErr := s.withItemHome(id, p, func(it Item) error {
		return s.store.ClearPicture(it.ID)
	})
	s.answerPictureWrite(w, id, found, allowed, opErr, "remove picture: ")
}

// withItemHome runs a write against one product, under the gate that changing the
// product runs under: its home catalogue must exist, be readable and be editable by
// the caller.
//
// A home the caller cannot see reads as absent, as everywhere else — a refusal
// saying "it exists, just not for you" answers the question the gate withholds.
func (s *Service) withItemHome(id string, p *httpapi.Principal, write func(Item) error) (
	found, allowed bool, opErr error) {
	s.loop.Do(func() {
		var it Item
		if it, found, opErr = s.store.Item(id); opErr != nil || !found {
			return
		}
		var home Catalog
		var homeOK bool
		if home, homeOK, opErr = s.store.Catalog(it.HomeCatalog); opErr != nil || !homeOK {
			found = false
			return
		}
		if found = s.mayRead(home, p); !found {
			return
		}
		if allowed = s.mayEdit(home, p); !allowed {
			return
		}
		opErr = write(it)
	})
	return found, allowed, opErr
}

// answerPictureWrite writes the one response shape both writes share.
func (s *Service) answerPictureWrite(w http.ResponseWriter, id string,
	found, allowed bool, opErr error, what string) {
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, what+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no product "+id)
	case !allowed:
		httpapi.Error(w, http.StatusForbidden,
			"a product's picture is set by whoever maintains its catalogue")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

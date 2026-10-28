package catalog

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/api/brandimage"
	"github.com/pblumer/atlas/api/contentsource"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// The images a product's rich description shows (ADR-0491, amendment of
// 2026-10-28).
//
// Copied out of the source at a fetch, so the shop never asks the source for
// anything, and named by the hash of their bytes. They are files beside the
// product's picture, for the reason the picture is (ADR-0391): bytes are not a
// record. A refresh adds the images it copied and removes none: a release frozen
// before it names the images it was published with, and the newest release keeps
// showing them until the next publish. The same image in two languages or two
// refreshes is one file.

// contentImagePath is where one image of an item's description is stored. The item
// id is hex-encoded as the picture's is; the image id is hex by construction.
func (s *Store) contentImagePath(itemID, imageID, ext string) string {
	return filepath.Join(s.pictures, hex.EncodeToString([]byte(itemID))+".c."+imageID+"."+ext)
}

// SaveContentImages writes the images a fetch copied. One already stored is left
// as it is: its name is its content.
func (s *Store) SaveContentImages(itemID string, images map[string]*contentsource.Picture) error {
	for id, pic := range images {
		if !contentsource.ImageID.MatchString(id) || pic == nil {
			continue
		}
		ext, ok := brandimage.Picture.ExtFor(pic.Type)
		if !ok {
			return fmt.Errorf("catalogstore: unsupported image type %q", pic.Type)
		}
		path := s.contentImagePath(itemID, id, ext)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := sidecar.WriteFile(s.pictures, path, pic.Data); err != nil {
			return err
		}
	}
	return sidecar.FsyncDir(s.pictures)
}

// ContentImage reads one image of an item's description.
func (s *Store) ContentImage(itemID, imageID string) (data []byte, contentType string, ok bool, err error) {
	if !contentsource.ImageID.MatchString(imageID) {
		return nil, "", false, nil
	}
	for _, ext := range brandimage.Picture.Exts() {
		b, readErr := os.ReadFile(s.contentImagePath(itemID, imageID, ext))
		if readErr == nil {
			ct, _ := brandimage.Picture.TypeFor(ext)
			return b, ct, true, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, "", false, fmt.Errorf("catalogstore: read image: %w", readErr)
		}
	}
	return nil, "", false, nil
}

// HandleGetContentImage serves one image of a product's description, behind the
// gate the product's picture is behind: whoever may see the product, and only while
// its content may be shown — a quarantine or a recheck gone stale withholds it as it
// withholds the text.
func (s *Service) HandleGetContentImage(w http.ResponseWriter, r *http.Request) {
	id, image := r.PathValue("id"), r.PathValue("image")
	p := httpapi.PrincipalFrom(r.Context())
	var (
		data      []byte
		ct        string
		found, ok bool
		opErr     error
	)
	s.loop.Do(func() {
		var it Item
		if it, found, opErr = s.store.Item(id); opErr != nil || !found {
			return
		}
		if found = s.maySeePicture(it.ID, it.HomeCatalog, p); !found {
			return
		}
		st, stFound, e := s.store.ContentState(id)
		if opErr = e; opErr != nil || !pictureShown(it, st, stFound, s.now()) {
			return
		}
		data, ct, ok, opErr = s.store.ContentImage(id, image)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read image: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no product "+id)
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "product "+id+" has no image "+image)
	default:
		brandimage.Serve(w, ct, data)
	}
}

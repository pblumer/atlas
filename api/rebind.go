package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// Moving a converted product's old lines onto its lifecycle process (ADR-0427).
//
// A line freezes the processes its product bound when it was placed, and by default
// keeps them: a grant is undone by the rules in force when it was made. That stops
// being fidelity when the target system behind the old process was replaced and the
// old process can no longer succeed. For that case a maintainer moves the product's
// lines to its current lifecycle binding — explicitly, never as a side effect of
// converting — and every moved line records what it was bound to, who moved it, when
// and why.

type rebindReq struct {
	Reason string `json:"reason"`
}

type rebindResp struct {
	Lines  int      `json:"lines"`
	Orders []string `json:"orders"`
}

func (s *Server) handleRebindProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req rebindReq
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}
	}
	if strings.TrimSpace(req.Reason) == "" {
		httpapi.Error(w, http.StatusBadRequest, "reason is required: a moved line has to say why "+
			"it no longer revokes by the rules it was granted under")
		return
	}
	p := httpapi.PrincipalFrom(r.Context())

	var (
		found, allowed, lifecycle bool
		opErr                     error
	)
	s.do(func() {
		it, ok, err := s.catalogStore.Item(id)
		if err != nil || !ok {
			opErr = err
			return
		}
		found = true
		cat, ok, err := s.catalogStore.Catalog(it.HomeCatalog)
		if err != nil {
			opErr = err
			return
		}
		allowed = ok && s.catalogs.MayEdit(cat, p)
		lifecycle = it.UsesLifecycleProcess()
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "rebind: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no product "+id)
		return
	case !allowed:
		httpapi.Error(w, http.StatusForbidden, "not an editor of product "+id+"'s home catalogue")
		return
	case !lifecycle:
		httpapi.Error(w, http.StatusConflict, "product "+id+" binds no lifecycle process; there is "+
			"nothing to move its lines to")
		return
	}

	// Which orders carry the product is a scan, so it is asked off the loop; each
	// order is then read and written again inside the loop, where the move happens.
	all, err := s.orderStore.All()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "rebind: "+err.Error())
		return
	}
	var candidates []string
	for _, o := range all {
		for _, l := range o.Lines {
			if l.ItemID == id && l.LifecycleProcess == "" {
				candidates = append(candidates, o.ID)
				break
			}
		}
	}

	resp := rebindResp{Orders: []string{}}
	by := principalID(r)
	s.do(func() {
		it, ok, err := s.catalogStore.Item(id)
		if err != nil || !ok {
			opErr = err
			return
		}
		for _, oid := range candidates {
			o, ok, err := s.orderStore.Get(oid)
			if err != nil {
				opErr = err
				return
			}
			if !ok {
				continue
			}
			next, moved := order.Rebind(o, it, by, req.Reason, s.now())
			if moved == 0 {
				continue
			}
			if opErr = s.orderStore.Save(next); opErr != nil {
				return
			}
			resp.Lines += moved
			resp.Orders = append(resp.Orders, oid)
		}
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "rebind: "+opErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

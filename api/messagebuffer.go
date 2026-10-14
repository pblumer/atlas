package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The message buffer as an operator reads it (ADR-0370).
//
// A message published with a time to live that nothing waited for is kept until a
// subscription opens for it or its time is up. Without a way to read what is kept, an
// instance that receives a message "from the past" is a mystery and a message that never
// arrives is invisible: so the buffer is listed here, a bounded page at a time, read off
// the run loop like every listing that grows with the data (ADR-0239).

const (
	bufferedPageDefault = 100
	bufferedPageMax     = 1000
)

// bufferedMessageView is one waiting message.
type bufferedMessageView struct {
	Key            string         `json:"key"` // the message's key, as a string so a script never rounds it
	Name           string         `json:"name"`
	CorrelationKey string         `json:"correlationKey"`
	MessageId      string         `json:"messageId,omitempty"`
	BufferedAt     int64          `json:"bufferedAt"` // Unix nanoseconds; 0 for one kept before it was recorded
	ExpiresAt      int64          `json:"expiresAt"`  // Unix nanoseconds
	Expired        bool           `json:"expired"`    // past its time, waiting only for the sweep: nothing receives it now
	Variables      []variableView `json:"variables"`
}

type bufferedMessagesResp struct {
	Messages []bufferedMessageView `json:"messages"`
	// Total is every message the buffer holds, whatever the page and the filter show.
	Total int64 `json:"total"`
	// NextCursor continues the listing; empty when this page was the last.
	NextCursor string `json:"nextCursor,omitempty"`
}

// handleBufferedMessages lists the messages waiting in the buffer, oldest first within
// a message name and correlation key: ?name= narrows to one message name, and with it
// ?correlationKey= to one key; ?limit= bounds the page (default 100, at most 1000) and
// ?after= continues from a previous page's nextCursor.
func (s *Server) handleBufferedMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("name")
	key := q.Get("correlationKey")
	if key != "" && name == "" {
		httpapi.Error(w, http.StatusBadRequest, "correlationKey narrows a message name: give name as well")
		return
	}
	limit := bufferedPageDefault
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "invalid limit (want a positive integer)")
			return
		}
		limit = min(n, bufferedPageMax)
	}
	var after []byte
	if v := q.Get("after"); v != "" {
		raw, err := hex.DecodeString(v)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid after cursor")
			return
		}
		after = raw
	}

	// The server clock, which a test shares with the engine (withClock), decides what
	// is already past its time.
	now := s.now()
	out := bufferedMessagesResp{Messages: []bufferedMessageView{}}
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		total, err := rv.BufferedMessageCount()
		if err != nil {
			return err
		}
		out.Total = total
		next, err := rv.BufferedMessagesPage(name, key, after, limit, func(msgKey uint64, v *model.MessageValue) error {
			vars := make([]variableView, 0, len(v.Variables))
			for i := range v.Variables {
				vars = append(vars, toVariableView(&v.Variables[i]))
			}
			out.Messages = append(out.Messages, bufferedMessageView{
				Key:            strconv.FormatUint(msgKey, 10),
				Name:           v.MessageName,
				CorrelationKey: v.CorrelationKey,
				MessageId:      v.MessageId,
				BufferedAt:     v.BufferedAt,
				ExpiresAt:      v.ExpiresAt,
				Expired:        v.ExpiresAt <= now,
				Variables:      vars,
			})
			return nil
		})
		if next != nil {
			out.NextCursor = hex.EncodeToString(next)
		}
		return err
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the message buffer: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, out)
}

package googlesheets

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The inbound half's read (ADR-draft-google-inbound-watch). A watch on a Drive folder
// needs to list what is in it, which is not one of the eight authorable operations and
// must not become one: a model cannot ask for it, and the bridge does not go through
// the operation table to get it.

// driveFileFieldsList is what a listing is asked to return per file. Drive answers
// with ids alone unless asked, and these are the fields a correlation key or a started
// instance actually uses — the link above all, because that is what goes into the mail
// or the task description the process sends next.
const driveFileFieldsList = "files(id,name,mimeType,createdTime,modifiedTime,webViewLink,size),nextPageToken"

// FileQuery is one page of a Drive folder listing. Field is the timestamp the listing
// is ordered and bounded by — "createdTime" for a watch on new files, "modifiedTime"
// for one on changes — and After is the cursor's lower bound, zero meaning none.
// Descending reverses the order, which is what priming uses to find the newest file
// and stop.
type FileQuery struct {
	Folder     string
	Field      string
	After      time.Time
	Limit      int
	Descending bool
}

// ListFiles lists the files of one Drive folder. Trashed files are excluded: a watch
// on a drop folder is about what arrives in it, and a file somebody deleted arriving
// again as an event is not that.
//
// A folder is required. Without one the query would be every file the credential can
// see, which is never what a watch meant and is expensive to discover by having done
// it.
func (c *HTTPClient) ListFiles(ctx context.Context, q FileQuery) ([]map[string]any, error) {
	folder := FolderID(q.Folder)
	if folder == "" {
		return nil, fmt.Errorf("googlesheets: a folder watch has no folder")
	}
	field := driveTimeField(q.Field)
	terms := []string{"'" + folder + "' in parents", "trashed = false"}
	if !q.After.IsZero() {
		// Drive compares an RFC 3339 timestamp; seconds are the granularity it keeps.
		terms = append(terms, field+" > '"+q.After.UTC().Format(time.RFC3339)+"'")
	}
	order := field
	if q.Descending {
		order += " desc"
	}
	params := url.Values{
		"q":       {strings.Join(terms, " and ")},
		"orderBy": {order},
		"fields":  {driveFileFieldsList},
		// A shared drive is where a real drop folder lives, and Drive hides those from
		// a listing unless both flags are set — the failure being an empty answer
		// rather than an error, which is the kind a watch never recovers from.
		"supportsAllDrives":         {"true"},
		"includeItemsFromAllDrives": {"true"},
	}
	if q.Limit > 0 {
		params.Set("pageSize", strconv.Itoa(q.Limit))
	}
	got, err := c.call(ctx, http.MethodGet,
		c.conn.DriveBase+"/drive/v3/files?"+params.Encode(), nil, Request{Operation: "watch-folder"})
	if err != nil {
		return nil, err
	}
	obj, _ := got.(map[string]any)
	raw, _ := obj["files"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, f := range raw {
		if file, ok := f.(map[string]any); ok {
			out = append(out, file)
		}
	}
	return out, nil
}

// driveTimeField is the file timestamp a watch follows. Anything but modifiedTime is
// createdTime: a watch on new files is the default, and a field Drive does not know
// would fail every poll with a 400 rather than in the one place it can be corrected.
func driveTimeField(field string) string {
	if strings.TrimSpace(field) == "modifiedTime" {
		return "modifiedTime"
	}
	return "createdTime"
}

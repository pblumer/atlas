package workersdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Documents a job works with (ADR-0543).
//
// A file a process received is kept in the server's document store, and a variable holds
// a reference to it: {"$document": "doc_…", "name", "contentType", "size", "sha256"}. A
// handler that needs the file reads it with [ReadDocument], under the lease its job is
// held by — a worker reads no document of its own accord, only one the job's instance
// was given. A handler that produces a file stores it with [UploadDocument] and completes
// with the reference it gets back; the server binds it to the job's instance when the
// completion lands.

// ErrNoDocuments is what [ReadDocument] and [UploadDocument] answer outside a job a
// worker runs — a handler called directly in a test, say — where there is no lease to
// read under and no server to store to.
var ErrNoDocuments = errors.New("workersdk: no document access here: documents are read and stored for a job a worker runs")

type documentsKey struct{}

// documentAccess is what a job's context carries to reach the document store.
type documentAccess struct {
	read   func(ctx context.Context, id string) ([]byte, Document, error)
	upload func(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// Document describes a document read with [ReadDocument].
type Document struct {
	ID          string
	Name        string
	ContentType string
}

// ReadDocument reads the bytes of the document id for the job running in ctx.
func ReadDocument(ctx context.Context, id string) ([]byte, Document, error) {
	a, ok := ctx.Value(documentsKey{}).(documentAccess)
	if !ok || a.read == nil {
		return nil, Document{}, ErrNoDocuments
	}
	return a.read(ctx, id)
}

// UploadDocument stores data as a new document and answers the reference a variable
// holds. The document is the worker's until the job completes with the reference.
func UploadDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error) {
	a, ok := ctx.Value(documentsKey{}).(documentAccess)
	if !ok || a.upload == nil {
		return nil, ErrNoDocuments
	}
	return a.upload(ctx, name, contentType, data)
}

// DocumentID returns the id a value names when it is a document reference, and "" for
// anything else: a decoded JSON object or its JSON text.
func DocumentID(v any) string {
	switch x := v.(type) {
	case map[string]any:
		id, _ := x["$document"].(string)
		return id
	case string:
		var ref struct {
			ID string `json:"$document"`
		}
		if json.Unmarshal([]byte(x), &ref) == nil {
			return ref.ID
		}
	}
	return ""
}

// DocumentContext puts the document store within reach of j's handler. The worker does
// this for every job it runs; it is exported so a handler run outside the poll loop can
// be given the same access.
func (w *Worker) DocumentContext(ctx context.Context, j Job) context.Context {
	return context.WithValue(ctx, documentsKey{}, documentAccess{
		read: func(ctx context.Context, id string) ([]byte, Document, error) {
			payload, _ := json.Marshal(map[string]any{"worker": w.opts.ID, "leaseToken": j.LeaseToken})
			path := "/api/v1/jobs/" + strconv.FormatUint(j.JobKey, 10) + "/documents/" + url.PathEscape(id)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.opts.Server+path, bytes.NewReader(payload))
			if err != nil {
				return nil, Document{}, err
			}
			req.Header.Set("Content-Type", "application/json")
			w.authorize(req)
			resp, err := w.http.Do(req)
			if err != nil {
				return nil, Document{}, err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return nil, Document{}, readStatusError(path, resp)
			}
			// The server bounds a document by its document budget; the body is what it sent.
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, Document{}, err
			}
			name, _ := url.PathUnescape(resp.Header.Get("X-Atlas-Document-Name"))
			return data, Document{ID: id, Name: name, ContentType: resp.Header.Get("Content-Type")}, nil
		},
		upload: func(ctx context.Context, name, contentType string, data []byte) (map[string]any, error) {
			path := "/api/v1/documents?name=" + url.QueryEscape(name)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.opts.Server+path, bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			req.Header.Set("Content-Type", contentType)
			w.authorize(req)
			resp, err := w.http.Do(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusCreated {
				return nil, readStatusError("/api/v1/documents", resp)
			}
			var ref map[string]any
			dec := json.NewDecoder(resp.Body)
			dec.UseNumber()
			if err := dec.Decode(&ref); err != nil {
				return nil, fmt.Errorf("workersdk: read document reference: %w", err)
			}
			return ref, nil
		},
	})
}

// authorize adds the worker's bearer credential to a request it builds itself.
func (w *Worker) authorize(req *http.Request) {
	if w.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.opts.Token)
	}
}

// readStatusError turns a refused response into the error the rest of the client
// answers with.
func readStatusError(path string, resp *http.Response) error {
	var msg struct {
		Error string `json:"error"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&msg)
	return &statusError{path: path, status: resp.StatusCode, msg: msg.Error,
		noRoute: resp.StatusCode == http.StatusNotFound && decodeErr != nil}
}

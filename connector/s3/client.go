// Package s3 integrates an S3-compatible object store as a server-registered Atlas
// Worker Type: a BPMN S3 task performs one object operation — put one down, read a small
// one back, ask whether it is there, list what is under a prefix, copy one, delete one,
// or mint a time-limited URL somebody can open it with — against a configured S3 Worker
// via the job path (ADR-draft-s3-object-store-worker). It mirrors how the jira package
// delegates an issue-tracker step to a registry-managed instance (ADR-0201) and inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying the reserved [compiler.S3JobType]. The processor
//     never performs the outbound call itself, so it stays allocation-free (invariant I1)
//     and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls the store off the
//     processor goroutine and after fsync (invariant I2, never inside applyToState / I4),
//     and completes the job, writing what the store answered into the task's result
//     variable, which drives the token onward.
//   - The access key lives in a server-side [Registry] keyed by Worker name, so a model
//     refers to an S3 Worker by name only and never carries a credential
//     (ADR-0069/0168). Only what the task is *about* — the operation, the bucket, the key
//     and its values — is authored in the model.
//
// # Why this is a Worker Type and not a REST task
//
// S3 authenticates with AWS Signature Version 4: a canonical request is hashed, a signing
// key is derived by chaining HMAC-SHA256 over the date, the region, the service and the
// literal aws4_request, and the hash is signed with it. The REST Worker's auth surface is
// basic, bearer, an API key and OAuth2 client credentials (ADR-0067/0152), and FEEL has
// neither HMAC nor SHA-256. The generic path is *structurally* blocked, which is gate 2's
// first limb in ADR-0299 and the same shape of absence ADR-0235 found at Google's JWT
// assertion.
//
// # Bytes do not travel through the engine
//
// A process variable is capped at limits.Variable — one mebibyte — and everything written
// into one is in the event log for as long as the installation keeps history. So the
// operations that *address* an object (list, head, copy, delete) carry only metadata, and
// the two that would carry a document carry a URL instead: presign-get and presign-put are
// pure computation, no network call at all, and produce a link the browser that has the
// bytes uses directly against the store.
//
// The byte path still exists, because a 4 KB manifest a gateway branches on is a real
// case: put-object and get-object move content through a variable, bounded by
// [MaxObjectBytes] — the process variable's own budget — which refuses rather than
// truncates, because half a PDF is not a smaller PDF.
//
// # A presigned URL is a capability
//
// Whoever holds one can perform that one verb on that one key until it expires, with no
// further authentication, and it lands in a process variable like any other value — which
// means the event log. That is argued and bounded rather than avoided in
// ADR-draft-s3-object-store-worker: the default expiry is an hour, the ceiling is S3's own
// seven days, and the panel says so where the field is authored. What must never travel is
// the *standing* credential, and it does not: a [Job] has nowhere to put one.
//
// # Delivery
//
// Delivery is at-least-once. Every operation here is idempotent under replay — a put to a
// key overwrites with the same bytes, a copy and a delete restate a state rather than
// advance one, the three reads change nothing, and a presign makes no call. The one case
// that is not is an author's: a key built from a non-deterministic expression makes a
// retry write a *second* object, which is true of any at-least-once write and is said in
// the properties panel where the key is authored.
package s3

import (
	"context"
	"sort"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/limits"
)

// MaxObjectBytes bounds the two operations that move an object's content through a
// process variable.
//
// It *is* [limits.Limits.Variable] rather than a number of this package's own, and that
// is the whole point: the destination of a read is one process variable, so a cap that
// could differ from the variable's own would only move the failure one step later, to a
// place with less context — and an installation that raises the variable budget means to
// raise this with it. It is a function rather than a package constant because reading the
// registry at the call site is what makes it one number instead of two
// (limits.TestNoCeilingWithoutAName holds every bounded read to that).
//
// It refuses rather than truncates, for the reason entra.Request.MaxBytes gives: the magic
// is at the front of a file, so a truncated document passes every format check there is
// and lands as something nobody can explain.
func MaxObjectBytes() int64 { return limits.Default().Variable }

// The content encodings a model may author for the byte path. Text is the default: a
// process writing JSON, CSV or a letter into a bucket wants the characters it composed.
// Base64 is what makes a binary round-trip possible at all, and what a form's uploaded
// file already looks like.
//
// They are spelled here as well as in the compiler because the dependency runs one way —
// this package imports the compiler, so the compiler cannot import it — and the drift test
// TestS3OpsMatchTheWorkerType holds the two together.
const (
	EncodingText   = "text"
	EncodingBase64 = "base64"
)

// DefaultExpiresIn is how long a presigned URL is valid when a model authors no lifetime.
//
// An hour is chosen from the thing the URL is for: somebody opens a user task, reads the
// document it points at, and decides. It is long enough that a person who steps away comes
// back to a link that still works, and short enough that the capability in the event log
// has stopped being one by the time anybody reads the log.
const DefaultExpiresIn int32 = 3600

// MaxExpiresIn is the longest lifetime SigV4 query signing permits: seven days. A larger
// value is refused by the store with a message about the signature rather than about the
// expiry, so the compiler refuses it at deploy instead.
const MaxExpiresIn int32 = 7 * 24 * 60 * 60

// DefaultMaxKeys caps a listing that authors no cap. A thousand is S3's own page size, and
// it is also the point past which a listing has stopped being something a process reads
// and started being something it should be paging.
const DefaultMaxKeys int32 = 1000

// MaxListPageSize is the most keys S3's list endpoint returns in one call. Asking for more
// is not an error at the store — it silently returns a thousand — which is exactly the
// kind of quiet difference between what a model says and what happens that the compiler
// refuses at deploy instead.
const MaxListPageSize int32 = 1000

// Op describes one object operation: what a model must author for it, and what it is
// allowed to carry. Keeping this a table rather than a switch is what lets the compiler,
// the Modeler's panel and this worker agree on the same rules — a new operation is a row,
// not three edits that can disagree.
//
// The compiler holds its own copy (compiler.s3Ops) because the dependency runs one way:
// this package imports the compiler, so the compiler cannot import it. The behavioural
// drift test TestS3OpsMatchTheWorkerType is what keeps the two honest.
type Op struct {
	// NeedsBucket marks an operation that addresses a bucket — which every one does,
	// because there is nowhere else for an object to be. It is a field rather than an
	// assumption so the drift test checks it like any other rule.
	NeedsBucket bool
	// NeedsKey marks an operation that addresses one object by its key. Only a listing
	// does not: it addresses a prefix, which is a different thing and has its own field.
	NeedsKey bool
	// NeedsContent marks put-object, the one operation that carries a document.
	NeedsContent bool
	// TakesContentType marks an operation that states what the bytes are — the put that
	// carries them, and the presigned upload that binds the type the client must send.
	TakesContentType bool
	// TakesEncoding marks the two operations on the byte path, where a model says whether
	// the content is text or base64.
	TakesEncoding bool
	// TakesList marks the one operation that pages: prefix, delimiter, startAfter and
	// maxKeys apply to it and to nothing else.
	TakesList bool
	// NeedsSource marks copy-object, whose source bucket and key are the other half of
	// what it addresses.
	NeedsSource bool
	// TakesExpiry marks the two presigns, the only operations with a lifetime.
	TakesExpiry bool
	// TakesMetadata marks an operation whose request can carry user metadata and the
	// x-amz-* headers a store reads (server-side encryption, storage class).
	TakesMetadata bool
	// NeedsResult marks an operation whose whole point is what it returns: a read or a
	// presign that discards its answer is a call made for nothing.
	NeedsResult bool
	// TakesResult marks an operation that answers with something a model may keep.
	// delete-object is the one that does not — S3 answers it with 204, where a result
	// variable would name a value that is never written.
	TakesResult bool
	// Label describes the operation for an error message.
	Label string
}

// Ops is the operation table: what a process actually does with a document. It is
// deliberately not "every S3 API call" — what earns a row is a step a business process
// takes, which is why there is no bucket lifecycle, versioning configuration, ACL,
// replication rule or multipart upload here. Those are administration of the store, and
// the store's own console and the generic REST Worker (ADR-0067) remain the way to reach
// them.
var Ops = map[string]Op{
	"put-object": {
		NeedsBucket: true, NeedsKey: true, NeedsContent: true,
		TakesContentType: true, TakesEncoding: true, TakesMetadata: true,
		TakesResult: true, Label: "put an object",
	},
	"get-object": {
		NeedsBucket: true, NeedsKey: true, TakesEncoding: true,
		NeedsResult: true, TakesResult: true, Label: "read an object",
	},
	// head-object answers with an object carrying `exists` rather than with nothing when
	// the key is absent, and the difference matters: "is it there" is the *question* this
	// operation is asked, and an unwritten variable would leave a model reading whatever
	// that name held before — which is indistinguishable from an answer.
	"head-object": {
		NeedsBucket: true, NeedsKey: true,
		NeedsResult: true, TakesResult: true, Label: "read an object's metadata",
	},
	"list-objects": {
		NeedsBucket: true, TakesList: true,
		NeedsResult: true, TakesResult: true, Label: "list objects under a prefix",
	},
	"copy-object": {
		NeedsBucket: true, NeedsKey: true, NeedsSource: true, TakesMetadata: true,
		TakesResult: true, Label: "copy an object",
	},
	"delete-object": {
		NeedsBucket: true, NeedsKey: true, Label: "delete an object",
	},
	// The two presigns require a result variable for the reason the reads do, and more
	// plainly: a URL nothing keeps is a signature computed for nobody.
	"presign-get": {
		NeedsBucket: true, NeedsKey: true, TakesExpiry: true,
		NeedsResult: true, TakesResult: true, Label: "mint a download link",
	},
	"presign-put": {
		NeedsBucket: true, NeedsKey: true, TakesExpiry: true, TakesContentType: true,
		NeedsResult: true, TakesResult: true, Label: "mint an upload link",
	},
}

// OpNames lists the operations, sorted, for the error messages that have to say what was
// expected.
func OpNames() []string {
	out := make([]string, 0, len(Ops))
	for name := range Ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Request is one object operation with every authored value already resolved: the worker
// has evaluated the task's literal-or-FEEL values against the variables it sees, so what
// reaches a client is plain data.
//
// Which fields carry a value follows from Operation, and the compiler has already refused
// a model that set one the operation does not use — so a client may read the fields its
// operation names and ignore the rest.
type Request struct {
	Operation string
	// Bucket is the bucket every operation acts in.
	Bucket string
	// Key is the object key: the object put, read, copied to, deleted or signed for. A
	// listing has none — it addresses Prefix instead.
	Key string
	// Content is what put-object writes, in the form Encoding names.
	Content string
	// ContentType is what the bytes are: sent on a put, and bound into a presigned upload
	// so the client that uses the URL must send the same one.
	ContentType string
	// Encoding is EncodingText or EncodingBase64 — how Content is read on a put, and how
	// the body is returned on a get. The compiler has already applied the default, so the
	// runtime interprets nothing (I5).
	Encoding string
	// Prefix narrows a listing to the keys that start with it, which is what "search"
	// means against a store that has no query language.
	Prefix string
	// Delimiter rolls the keys that share a segment up into common prefixes — "/" makes a
	// listing read like a folder rather than like every object beneath one.
	Delimiter string
	// StartAfter is a listing's cursor: the continuation token a previous page answered
	// with, or a key to start after. One field for both because they occupy the same place
	// in a model — "carry on from here" — and the client sends whichever S3 wants.
	StartAfter string
	// MaxKeys caps what a listing may return. The compiler has already applied the default
	// and refused a value past what the endpoint honours.
	MaxKeys int32
	// SourceBucket and SourceKey are what copy-object copies from.
	SourceBucket string
	SourceKey    string
	// ExpiresIn is a presigned URL's lifetime in seconds. The compiler has already applied
	// the default and refused a value past [MaxExpiresIn].
	ExpiresIn int32
	// Metadata are extra request headers keyed by name, each carrying the text its FEEL
	// value resolved to. A name that is not already an x-amz-* header is sent as user
	// metadata (x-amz-meta-<name>), which is how a process tags an object with the case it
	// belongs to without this type naming every header S3 will ever read.
	Metadata map[string]string
}

// Client performs one operation against a configured S3 Worker. It is an interface so the
// worker is testable without a live object store and so a Worker name binds to exactly one
// identity.
//
// The shape is a single Do rather than a method per operation for the reason the Entra,
// Jira and Discord workers give (ADR-0172/0201): this is a typed façade over an HTTP API,
// and the value it adds is at the *model* level — naming the operations, addressing the
// bucket and signing the request — not in wrapping eight HTTP calls in eight Go
// signatures.
type Client interface {
	// Do performs one operation and returns what the store answered: the stored object's
	// identity, the read object with its content, the metadata and whether the object is
	// there, the listing page, the copy's result, the minted URL, or nil for the one
	// operation that answers with nothing (a delete).
	Do(ctx context.Context, req Request) (any, error)
}

// Registry resolves a Worker name to the [Client] for this kind. Workers are registered at
// the server from managed configuration (an access key in the vault), so a model refers to
// a Worker by name only (ADR-0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured Worker is
// missing from it — the difference between "never configured" and "configured and broken",
// which is what a parked token has to be able to say (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty Worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }

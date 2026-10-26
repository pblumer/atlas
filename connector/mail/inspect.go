package mail

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// Inspecting a document before it leaves the mailbox
// (ADR-0478).
//
// Anybody on the internet can write to an address, so every attachment is untrusted
// input written by a stranger — and the three operations that hand one on put it in
// front of a person: in a user task, in a bucket a presigned link opens, in somebody
// else's inbox. So nothing is handed on that has not been looked at, and looking has
// two layers that answer different questions.
//
// The built-in policy is deterministic and needs nothing installed. It decides what
// the bytes *are* from the bytes, never from the sender's name or content type — both
// are claims — and refuses the shapes mail-borne malware arrives in: executables and
// scripts under any name, a document whose name says one thing and whose content is
// another, Office macros, archives that hide their content behind a password or carry
// a program, PDFs that run code or launch one, and names that use bidirectional
// control characters to show "exe.pdf" as "fdp.exe". It does not claim to find
// malware: a PDF exploit inside a compressed stream passes it.
//
// The scanner is that second question, asked of a malware engine the installation
// runs — ClamAV's clamd, over its INSTREAM command, because it is the open engine
// every platform packages and its protocol needs no client library. A Worker that
// names one has every document scanned, and a scanner that cannot answer is never
// read as a clean answer: the job fails and is retried, then waits on an incident.

// CodeAttachmentRejected is the BPMN error a task ends with when inspection refuses a
// document and the task's process catches the code. Uncaught, the refusal is a
// failure like any other — retried, then an incident naming the reason — so a refused
// document is never handed on and never silently dropped (ADR-0461's rule, applied
// here).
const CodeAttachmentRejected = "MAIL_ATTACHMENT_REJECTED"

// Codes lists every code an operation of this Worker Type can throw.
var Codes = []string{CodeAttachmentRejected}

// Rejection is why a document may not be handed on. Name is the attachment's name as
// the sender wrote it, which a person needs to find the mail again.
type Rejection struct {
	Name   string
	Reason string
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("mail: attachment %q was not handed on: %s", r.Name, r.Reason)
}

// Verdict is what inspection found about a document that passed: what its bytes are,
// whether a browser would run them as a page, and whether a malware scanner looked.
// It travels with the document into the result variable and into the object store's
// metadata, so whoever presents the document later can see how far it was checked.
type Verdict struct {
	DetectedType string
	Active       bool
	Scanned      bool
	Scanner      string
}

// Fields is the verdict as a process variable.
func (v Verdict) Fields() map[string]any {
	return map[string]any{
		"detectedType": v.DetectedType,
		"active":       v.Active,
		"scanned":      v.Scanned,
		"scanner":      v.Scanner,
	}
}

// Scanner looks for malware in a document.
type Scanner interface {
	// Name is the engine that answered, for the verdict.
	Name() string
	// Scan answers the signature found, "" for none, or an error when it could not
	// look. An error is never a clean answer.
	Scan(ctx context.Context, data []byte) (string, error)
	// Ping checks the scanner answers at all, for the Worker's check button.
	Ping(ctx context.Context) error
}

// inspector applies the built-in policy and then the scanner, when there is one.
// allowed, when set, narrows the types a task accepts to the ones its model named.
type inspector struct {
	scanner Scanner
	allowed []string
}

// inspect decides whether a document may be handed on.
func (in inspector) inspect(ctx context.Context, d Document) (Verdict, error) {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = "attachment"
	}
	if why := policyRefusal(name, d.Data); why != "" {
		return Verdict{}, &Rejection{Name: name, Reason: why}
	}
	detected := sniffType(d.Data)
	if len(in.allowed) > 0 && !typeAllowed(detected, in.allowed) {
		return Verdict{}, &Rejection{Name: name, Reason: fmt.Sprintf(
			"its content is %s, which this task does not accept (allowedTypes: %s)", detected, strings.Join(in.allowed, ", "))}
	}
	v := Verdict{DetectedType: detected, Active: activeType(detected)}
	if in.scanner == nil {
		return v, nil
	}
	sig, err := in.scanner.Scan(ctx, d.Data)
	if err != nil {
		// Not a rejection: the document was not found to be malware, it was not looked
		// at. Failing the job retries it and then raises an incident, which is what an
		// operator whose scanner is down needs to hear.
		return Verdict{}, fmt.Errorf("mail: attachment %q could not be scanned, so it is not handed on: %w", name, err)
	}
	if sig != "" {
		return Verdict{}, &Rejection{Name: name, Reason: "the malware scanner (" + in.scanner.Name() + ") found " + sig}
	}
	v.Scanned, v.Scanner = true, in.scanner.Name()
	return v, nil
}

// blockedExtensions are the names Windows, macOS or a mail client opens as a program,
// a script, a shortcut, a disk image or a macro-carrying Office file on a
// double-click. A document arriving under one of them is refused whatever its content
// turns out to be: the name alone decides what the person's machine will do with it.
var blockedExtensions = map[string]bool{
	// Programs and libraries.
	"exe": true, "com": true, "scr": true, "pif": true, "cpl": true, "dll": true, "sys": true,
	"drv": true, "ocx": true, "xll": true, "msi": true, "msp": true, "mst": true,
	"appx": true, "appxbundle": true, "msix": true, "msixbundle": true, "application": true,
	"jar": true, "apk": true, "app": true, "dmg": true, "pkg": true, "run": true,
	// Scripts and things a shell executes.
	"bat": true, "cmd": true, "ps1": true, "psm1": true, "psd1": true, "ps1xml": true,
	"vbs": true, "vbe": true, "js": true, "jse": true, "wsf": true, "wsh": true, "wsc": true,
	"sct": true, "hta": true, "sh": true, "command": true, "reg": true, "inf": true,
	"scf": true, "gadget": true, "chm": true, "hlp": true, "settingcontent-ms": true,
	// Shortcuts and links that point a click somewhere else.
	"lnk": true, "url": true, "iqy": true, "library-ms": true,
	// Disk images, which carry the file inside past the mark-of-the-web.
	"iso": true, "img": true, "vhd": true, "vhdx": true,
	// Office files that may carry macros, and OneNote, which campaigns used once
	// macros were blocked.
	"docm": true, "dotm": true, "xlsm": true, "xltm": true, "xlam": true, "xlsb": true,
	"pptm": true, "potm": true, "ppam": true, "ppsm": true, "sldm": true, "one": true,
}

// policyRefusal answers why the built-in policy refuses a document, or "".
func policyRefusal(name string, data []byte) string {
	if hasBidiControl(name) {
		return "its name contains a bidirectional control character, which is how a program is made to look like a document"
	}
	ext := extension(name)
	if blockedExtensions[ext] {
		return fmt.Sprintf("files named .%s are programs, scripts, shortcuts, disk images or macro documents", ext)
	}
	switch {
	case isExecutable(data):
		return "its content is a program, whatever its name says"
	case bytes.HasPrefix(data, []byte("#!")):
		return "its content is a script, whatever its name says"
	case isOLE(data) && hasVBA(data):
		return "it is an Office document with macros"
	case bytes.HasPrefix(data, []byte("%PDF-")):
		if why := pdfRefusal(data); why != "" {
			return why
		}
	case isZip(data):
		if why := zipRefusal(data); why != "" {
			return why
		}
	}
	if why := disguised(ext, data); why != "" {
		return why
	}
	return ""
}

// extension is a name's last extension, lower-cased, with the trailing dots and spaces
// Windows ignores taken off first — "invoice.exe." opens as invoice.exe.
func extension(name string) string {
	clean := strings.TrimRight(strings.ToLower(strings.TrimSpace(name)), ". ")
	return strings.TrimPrefix(path.Ext(clean), ".")
}

func hasBidiControl(s string) bool {
	for _, r := range s {
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F {
			return true
		}
	}
	return false
}

// isExecutable recognises the program formats of the three desktop platforms: PE
// (Windows), ELF (Linux) and Mach-O (macOS, including the fat binary, whose magic Java
// class files share — code either way).
//
// "MZ" alone is two letters a CSV can start with, so a DOS/PE header is recognised by
// the NUL bytes its fixed fields always carry within the first 64, which no text has.
func isExecutable(data []byte) bool {
	switch {
	case bytes.HasPrefix(data, []byte("MZ")) && bytes.IndexByte(head(data, 64), 0) >= 0:
		return true
	case bytes.HasPrefix(data, []byte("\x7fELF")):
		return true
	case bytes.HasPrefix(data, []byte{0xFE, 0xED, 0xFA, 0xCE}), bytes.HasPrefix(data, []byte{0xFE, 0xED, 0xFA, 0xCF}),
		bytes.HasPrefix(data, []byte{0xCE, 0xFA, 0xED, 0xFE}), bytes.HasPrefix(data, []byte{0xCF, 0xFA, 0xED, 0xFE}),
		bytes.HasPrefix(data, []byte{0xCA, 0xFE, 0xBA, 0xBE}):
		return true
	case bytes.HasPrefix(data, []byte{0x4C, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00}): // a Windows shortcut
		return true
	}
	return false
}

var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// isOLE recognises the compound file the legacy Office formats (.doc, .xls, .ppt) and
// Windows Installer packages are stored in.
func isOLE(data []byte) bool { return bytes.HasPrefix(data, oleMagic) }

// hasVBA reports whether a compound file carries a VBA project: every one has a
// _VBA_PROJECT stream (MS-OVBA §2.3.4.1), and directory names are UTF-16 in the file,
// so that is the encoding looked for. It is a heuristic — the name could be spelled
// inside a document's own text — and it errs on the side of refusing, which for a
// macro is the side to err on.
func hasVBA(data []byte) bool { return bytes.Contains(data, utf16le("_VBA_PROJECT")) }

func utf16le(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, s[i], 0)
	}
	return out
}

func isZip(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06"))
}

// zipRefusal inspects an archive's directory — every Office Open XML file is one too.
// It refuses a VBA project (a macro document under an innocent extension), an
// encrypted entry (nothing can look inside it, which is why malware is mailed that
// way), and a program or script anywhere in it. Nested archives are not opened: that
// is the scanner's work, and a policy that unpacked archives would be a decompression
// bomb's best friend.
func zipRefusal(data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "it claims to be an archive but its directory cannot be read"
	}
	hasManifest, hasClass, hasDex := false, false, false
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		switch {
		case f.Flags&0x1 != 0:
			return "it is an encrypted archive, which nothing can look inside"
		case strings.HasSuffix(lower, "vbaproject.bin"):
			return "it is an Office document with macros"
		case blockedExtensions[extension(lower)]:
			return fmt.Sprintf("the archive contains %s", path.Base(f.Name))
		case lower == "meta-inf/manifest.mf":
			hasManifest = true
		case lower == "classes.dex":
			hasDex = true
		case strings.HasSuffix(lower, ".class"):
			hasClass = true
		}
	}
	switch {
	case hasManifest && hasClass:
		return "it is a Java program"
	case hasDex:
		return "it is an Android app"
	}
	return ""
}

// pdfRefusal refuses a PDF that runs code or launches a program: a /JavaScript (or
// /JS) action, or a /Launch action. Names are read with their #xx escapes decoded,
// because "/J#61vaScript" is the oldest trick against a search for "/JavaScript". It
// cannot see into compressed object streams — the scanner's half of the work — and it
// refuses interactive forms whose calculations are scripts, which is a price paid on
// purpose: a form a person needs can still be opened from the mailbox itself.
func pdfRefusal(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '/' {
			continue
		}
		j := i + 1
		var name []byte
		for j < len(data) && !pdfDelimiter(data[j]) && len(name) < 32 {
			c := data[j]
			if c == '#' && j+2 < len(data) {
				if v, ok := hexByte(data[j+1], data[j+2]); ok {
					name = append(name, v)
					j += 3
					continue
				}
			}
			name = append(name, c)
			j++
		}
		switch string(name) {
		case "JavaScript", "JS":
			return "it is a PDF that runs JavaScript"
		case "Launch":
			return "it is a PDF that launches a program"
		}
		i = j - 1
	}
	return ""
}

func pdfDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f', 0, '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func hexByte(a, b byte) (byte, bool) {
	hi, ok1 := hexNibble(a)
	lo, ok2 := hexNibble(b)
	return hi<<4 | lo, ok1 && ok2
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// disguised refuses a document whose name names a format its content is not: an
// "invoice.pdf" that is no PDF, a "scan.jpg" that is no image. A person trusts the
// name; the content is what their machine opens.
func disguised(ext string, data []byte) string {
	detected := sniffType(data)
	want := ""
	ok := true
	switch ext {
	case "pdf":
		want, ok = "a PDF", detected == "application/pdf"
	case "png", "jpg", "jpeg", "gif", "webp", "bmp":
		want, ok = "an image", strings.HasPrefix(detected, "image/")
	case "docx", "xlsx", "pptx":
		want, ok = "an Office document", strings.HasPrefix(detected, "application/vnd.openxmlformats-officedocument.")
	case "doc", "xls", "ppt":
		want, ok = "an Office document", detected == "application/x-ole-storage"
	case "txt", "csv":
		want, ok = "text", strings.HasPrefix(detected, "text/")
	}
	if ok {
		return ""
	}
	return fmt.Sprintf("it is named .%s but its content is not %s (it is %s)", ext, want, detected)
}

// sniffType decides what a document is from its bytes: the standard library's
// content sniffing, completed with the formats it does not know and that matter here.
// The answer carries no parameters, so it can be compared and stored.
func sniffType(data []byte) string {
	switch {
	case len(data) == 0:
		return "application/octet-stream"
	case isOLE(data):
		return "application/x-ole-storage"
	case isZip(data):
		return zipType(data)
	}
	mt := http.DetectContentType(data)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	mt = strings.TrimSpace(mt)
	// The standard sniffer calls an SVG text or XML. Only a document already sniffed as
	// markup is looked at for it, so a PDF or an image that happens to carry the word
	// keeps the type its own magic gave it.
	if (mt == "text/xml" || mt == "text/plain" || mt == "text/html") && bytes.Contains(head(data, 1024), []byte("<svg")) {
		return "image/svg+xml"
	}
	return mt
}

func head(data []byte, n int) []byte {
	if len(data) > n {
		return data[:n]
	}
	return data
}

// zipType names an Office Open XML document by the part its package is built around;
// any other archive is a zip.
func zipType(data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "application/zip"
	}
	for _, f := range zr.File {
		switch strings.ToLower(f.Name) {
		case "word/document.xml":
			return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case "xl/workbook.xml":
			return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		case "ppt/presentation.xml":
			return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		}
	}
	return "application/zip"
}

// activeType reports whether a browser opening the document would run it as a page:
// HTML, SVG and XML can carry script. Such a document is still handed on — receipts
// arrive as HTML — but it is stored to download rather than to open.
func activeType(mt string) bool {
	switch mt {
	case "text/html", "image/svg+xml", "text/xml", "application/xml":
		return true
	}
	return false
}

// typeAllowed matches a detected type against a task's allowedTypes: an exact type, or
// "image/*" for a whole family.
func typeAllowed(detected string, allowed []string) bool {
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
		case a == detected:
			return true
		case strings.HasSuffix(a, "/*") && strings.HasPrefix(detected, strings.TrimSuffix(a, "*")):
			return true
		}
	}
	return false
}

// SplitTypes reads a task's allowedTypes value: comma-separated media types, each
// either exact or a family ("image/*"). Blanks are dropped.
func SplitTypes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.ToLower(strings.TrimSpace(p)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// --- the scanner ---

// ScannerSchemeClamd is the one scanner scheme: ClamAV's daemon, reached over TCP
// ("clamd://host:3310") or a Unix socket ("clamd:///run/clamav/clamd.ctl").
const ScannerSchemeClamd = "clamd"

const clamdDefaultPort = "3310"

// NormalizeScannerEndpoint canonicalizes an operator-written scanner endpoint, or
// explains why it cannot. A bare host means clamd over TCP on its default port.
func NormalizeScannerEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = ScannerSchemeClamd + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, ScannerSchemeClamd) {
		return "", fmt.Errorf("scanner %q: want clamd://host[:port] or clamd:///path/to/clamd.socket", raw)
	}
	if u.Host == "" {
		if u.Path == "" || u.Path == "/" {
			return "", fmt.Errorf("scanner %q: names neither a host nor a socket", raw)
		}
		return ScannerSchemeClamd + "://" + u.Path, nil
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), clamdDefaultPort)
	}
	return ScannerSchemeClamd + "://" + host, nil
}

// NewScanner builds the scanner an endpoint names; "" builds none.
func NewScanner(endpoint string) (Scanner, error) {
	norm, err := NormalizeScannerEndpoint(endpoint)
	if err != nil || norm == "" {
		return nil, err
	}
	rest := strings.TrimPrefix(norm, ScannerSchemeClamd+"://")
	if strings.HasPrefix(rest, "/") {
		return &clamdScanner{network: "unix", addr: rest}, nil
	}
	return &clamdScanner{network: "tcp", addr: rest}, nil
}

// clamdScanner speaks clamd's INSTREAM command: the document in length-prefixed
// chunks, a zero-length chunk to end it, and one line of answer — "stream: OK",
// "stream: <signature> FOUND", or an ERROR, which includes clamd's own
// StreamMaxLength refusing a document larger than it is configured to scan.
type clamdScanner struct {
	network, addr string
}

// clamdChunk is how much of the document goes in one INSTREAM chunk.
const clamdChunk = 64 << 10

func (c *clamdScanner) Name() string { return "clamav" }

func (c *clamdScanner) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, c.network, c.addr)
	if err != nil {
		return nil, fmt.Errorf("clamd at %s: %w", c.addr, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(nettimeout.Default)
	}
	_ = conn.SetDeadline(deadline)
	return conn, nil
}

func (c *clamdScanner) Scan(ctx context.Context, data []byte) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", fmt.Errorf("clamd at %s: %w", c.addr, err)
	}
	var size [4]byte
	for off := 0; off < len(data); off += clamdChunk {
		end := min(off+clamdChunk, len(data))
		binary.BigEndian.PutUint32(size[:], uint32(end-off))
		if _, err := conn.Write(size[:]); err != nil {
			return "", fmt.Errorf("clamd at %s: %w", c.addr, err)
		}
		if _, err := conn.Write(data[off:end]); err != nil {
			return "", fmt.Errorf("clamd at %s: %w", c.addr, err)
		}
	}
	binary.BigEndian.PutUint32(size[:], 0)
	if _, err := conn.Write(size[:]); err != nil {
		return "", fmt.Errorf("clamd at %s: %w", c.addr, err)
	}
	answer, err := c.answer(conn)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(strings.TrimPrefix(answer, "stream:"))
	switch {
	case answer == "OK":
		return "", nil
	case strings.HasSuffix(answer, " FOUND"):
		return strings.TrimSpace(strings.TrimSuffix(answer, " FOUND")), nil
	}
	return "", fmt.Errorf("clamd at %s answered %q", c.addr, answer)
}

func (c *clamdScanner) Ping(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("clamd at %s: %w", c.addr, err)
	}
	answer, err := c.answer(conn)
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) != "PONG" {
		return fmt.Errorf("clamd at %s answered %q to PING", c.addr, answer)
	}
	return nil
}

// answer reads clamd's reply, which the z-prefixed commands end with a NUL. It is a
// line, so it is read under the error-quotation budget.
func (c *clamdScanner) answer(conn net.Conn) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(conn, limits.Default().ErrorBody))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("clamd at %s: %w", c.addr, err)
	}
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("clamd at %s closed the connection without an answer", c.addr)
	}
	return string(raw), nil
}

// --- a Worker with a scanner ---

// scanningClient is a provider client with a malware scanner beside it. It is a
// wrapper rather than a field on every provider because the scanner belongs to the
// Worker, not to SMTP, Gmail or Graph — and a wrapper is the one place that has to
// pass the provider's own abilities (its mailbox, its check) through.
type scanningClient struct {
	Client
	scanner Scanner
}

// WithScanner returns c with s beside it; a nil s returns c unchanged.
func WithScanner(c Client, s Scanner) Client {
	if s == nil || c == nil {
		return c
	}
	return &scanningClient{Client: c, scanner: s}
}

// Mailbox is the wrapped provider's mailbox.
func (c *scanningClient) Mailbox() (Mailbox, error) { return MailboxOf(c.Client) }

// AttachmentScanner is the Worker's scanner.
func (c *scanningClient) AttachmentScanner() Scanner { return c.scanner }

// Probe checks the provider and then the scanner: a Worker whose scanner does not
// answer is a Worker every attachment operation will fail on, which is exactly what
// the check button is for.
func (c *scanningClient) Probe(ctx context.Context) error {
	if err := Probe(ctx, c.Client); err != nil {
		return err
	}
	if err := c.scanner.Ping(ctx); err != nil {
		return fmt.Errorf("mail: the Worker's malware scanner does not answer: %w", err)
	}
	return nil
}

// ScannerOf answers the scanner a Worker's client carries, or nil.
func ScannerOf(c Client) Scanner {
	if s, ok := c.(interface{ AttachmentScanner() Scanner }); ok {
		return s.AttachmentScanner()
	}
	return nil
}

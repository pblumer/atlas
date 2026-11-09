package sharepointserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
	"github.com/pblumer/atlas/limits"
)

const (
	// defaultTimeout bounds one job. Creating a site collection provisions a whole
	// template, which takes minutes on a small farm, so it is longer than Exchange's
	// bound; it is still a bound, so a farm that hangs fails the job visibly.
	defaultTimeout = 10 * time.Minute
	// defaultPowerShell is Windows PowerShell: no Microsoft page we could read says the
	// SharePoint commands load in PowerShell 7, and the Management Shell is 5.1.
	defaultPowerShell = "powershell.exe"
	// defaultManagedPath is the wildcard managed path a team site lives under.
	defaultManagedPath = "sites"
)

// Runner starts a program and returns what it wrote, so the client is testable without
// PowerShell or a farm.
type Runner interface {
	Run(ctx context.Context, bin string, args, env []string) (stdout, stderr []byte, err error)
}

// Client runs one operation on the farm this worker is a server of.
type Client struct {
	web        string
	path       string
	powershell string
	timeout    time.Duration
	run        Runner
	environ    func() []string
}

func newClient(web, path, powershell string, timeout time.Duration) *Client {
	return &Client{web: web, path: path, powershell: powershell, timeout: timeout, run: execRunner{}, environ: os.Environ}
}

// request is what the bootstrap reads from ATLAS_SHAREPOINTSERVER_REQUEST.
type request struct {
	Op             string         `json:"op"`
	WebApplication string         `json:"webApplication"`
	SiteURL        string         `json:"siteUrl,omitempty"`
	SitePath       string         `json:"sitePath,omitempty"`
	Args           map[string]any `json:"args"`
}

// answer is the one line the bootstrap writes.
type answer struct {
	OK    bool   `json:"ok"`
	Value *[]any `json:"value"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Invoke runs one checked operation in a fresh PowerShell: a process per call, for
// Exchange's reason, and because SharePoint's objects hold unmanaged memory that a
// process ending frees reliably.
func (c *Client) Invoke(ctx context.Context, inv catalog.Invocation) ([]any, error) {
	req := request{Op: inv.Op, WebApplication: c.web, Args: inv.Args}
	if inv.Site != "" {
		req.SitePath = "/" + c.path + "/" + inv.Site
		req.SiteURL = c.web + req.SitePath
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sharepointserver: encode %s: %w", inv.Op, err)
	}
	env := append(childEnv(c.environ()), "ATLAS_SHAREPOINTSERVER_REQUEST="+string(raw))
	args := []string{"-NoProfile", "-NonInteractive", "-NoLogo", "-EncodedCommand", encodeCommand(bootstrap)}
	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	stdout, stderr, runErr := c.run.Run(runCtx, c.powershell, args, env)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, fmt.Errorf("sharepointserver: %s did not finish within %s; the operation may still complete on the farm", inv.Op, c.timeout)
	}
	ans, ok := lastAnswer(stdout)
	if !ok {
		quote := strings.TrimSpace(string(stderr))
		if quote == "" {
			quote = strings.TrimSpace(string(stdout))
		}
		if max := int(limits.Default().ErrorBody); len(quote) > max {
			quote = quote[:max] + "…"
		}
		if runErr != nil {
			return nil, fmt.Errorf("sharepointserver: %s: %s could not run the call (%v): %s", inv.Op, c.powershell, runErr, quote)
		}
		return nil, fmt.Errorf("sharepointserver: %s: %s wrote something that is not the bootstrap's answer: %s", inv.Op, c.powershell, quote)
	}
	if !ans.OK {
		if ans.Error == nil {
			return nil, fmt.Errorf("sharepointserver: %s failed without saying why", inv.Op)
		}
		msg := fmt.Sprintf("sharepointserver: %s failed: %s", inv.Op, strings.TrimSpace(ans.Error.Message))
		if ans.Error.Code != "" {
			return nil, &Refusal{Code: ans.Error.Code, Message: msg}
		}
		return nil, errors.New(msg)
	}
	if ans.Value == nil {
		return nil, fmt.Errorf("sharepointserver: %s answered without a %q list", inv.Op, "value")
	}
	return *ans.Value, nil
}

// lastAnswer reads the bootstrap's answer, the last non-empty line of its output: a
// snap-in that prints something on load must not be mistaken for it.
func lastAnswer(stdout []byte) (answer, bool) {
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, "{") {
		return answer{}, false
	}
	var ans answer
	dec := json.NewDecoder(strings.NewReader(last))
	dec.UseNumber()
	if err := dec.Decode(&ans); err != nil {
		return answer{}, false
	}
	return ans, true
}

// childEnv is the worker's environment without any ATLAS_ variable: those carry the
// worker's Atlas token, none of which this child needs.
func childEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if !strings.HasPrefix(strings.ToUpper(kv), "ATLAS_") {
			out = append(out, kv)
		}
	}
	return out
}

// encodeCommand is the form -EncodedCommand takes: base64 of UTF-16LE.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2*i:], r)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// execRunner is the production [Runner].
type execRunner struct{}

func (execRunner) Run(ctx context.Context, bin string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	stdout := &capped{max: limits.Default().Payload}
	stderr := &capped{max: limits.Default().ErrorBody}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if stdout.over {
		return nil, stderr.Bytes(), fmt.Errorf("the answer was larger than the %d byte payload budget", stdout.max)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// capped keeps the first max bytes written to it and discards the rest.
type capped struct {
	bytes.Buffer
	max  int64
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - int64(c.Len()); room > 0 {
		if int64(len(p)) > room {
			c.Buffer.Write(p[:room])
			c.over = true
		} else {
			c.Buffer.Write(p)
		}
	} else if len(p) > 0 {
		c.over = true
	}
	return len(p), nil
}

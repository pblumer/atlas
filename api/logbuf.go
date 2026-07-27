package api

import (
	"strings"
	"sync"
)

// LogBuffer is a bounded, concurrency-safe ring of the most recent process log
// lines. The command tees the standard logger into it
// (log.SetOutput(io.MultiWriter(os.Stderr, buf))) and hands it to the server via
// WithLogBuffer, so an operator can read recent server logs over the web — e.g. to
// check the script-worker startup lines — without shell access to the host.
//
// It only retains the last max lines, so it is a diagnostic tail, not durable
// storage; it holds no secrets beyond whatever the process already logs.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// NewLogBuffer creates a LogBuffer retaining the last max lines (defaulting to
// 1000 when max <= 0).
func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 1000
	}
	return &LogBuffer{max: max}
}

// Write appends the (possibly multi-line) log output, keeping only the most recent
// max lines. It never errors and always reports the full length consumed, so it is
// safe as an io.Writer log sink behind io.MultiWriter.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.lines = append(b.lines, line)
	}
	if over := len(b.lines) - b.max; over > 0 {
		// Compact the tail to the front so the backing array stays bounded.
		b.lines = append(b.lines[:0], b.lines[over:]...)
	}
	return len(p), nil
}

// Lines returns a copy of the buffered log lines, oldest first.
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

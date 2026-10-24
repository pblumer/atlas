package storage

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/internal/diskspace"
)

const mib = 1 << 20

// disk is a scripted file system: each directory reads as the volume it is mapped to,
// and a volume reads whatever the test last set for it.
type disk struct {
	volumeOf map[string]string
	usage    map[string]diskspace.Usage
	err      error
}

func (d *disk) read(dir string) (diskspace.Usage, error) {
	if d.err != nil {
		return diskspace.Usage{}, d.err
	}
	v, ok := d.volumeOf[dir]
	if !ok {
		return diskspace.Usage{}, errors.New("no such directory: " + dir)
	}
	u := d.usage[v]
	u.Volume = v
	return u, nil
}

func (d *disk) set(volume string, total, available uint64) {
	d.usage[volume] = diskspace.Usage{Total: total, Free: available, Available: available}
}

// clock is a manual clock the monitor reads its sampling times from.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)} }
func oneDisk() *disk {
	return &disk{volumeOf: map[string]string{"/data": "v1", "/data/wal": "v1", "/data/state": "v1"}, usage: map[string]diskspace.Usage{}}
}
func monitorOver(d *disk, c *clock, reserve uint64) *Monitor {
	return New(Config{
		Dirs:    []string{"/data", "/data/wal", "/data/state"},
		Reserve: reserve,
		Read:    d.read,
		Now:     c.now,
	})
}

// TestFreezesBelowTheReserveAndResumesAboveTheMargin: below the reserve the server
// stops taking new writes; it takes them again only once a quarter of the reserve more
// is free, so a disk hovering at the line does not flip it on every reading.
func TestFreezesBelowTheReserveAndResumesAboveTheMargin(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)

	steps := []struct {
		available uint64
		frozen    bool
		why       string
	}{
		{500 * mib, false, "plenty free"},
		{99 * mib, true, "below the reserve"},
		{110 * mib, true, "above the reserve but inside the margin: still frozen"},
		{125 * mib, false, "reserve plus a quarter of it: resumes"},
		{101 * mib, false, "above the reserve: stays open"},
		{100*mib - 1, true, "one byte short: frozen again"},
	}
	for _, s := range steps {
		d.set("v1", 1000*mib, s.available)
		c.advance(5 * time.Second)
		m.Sample()
		if got := m.Frozen(); got != s.frozen {
			t.Fatalf("%s (%d MiB available): Frozen() = %v, want %v", s.why, s.available/mib, got, s.frozen)
		}
		want := StateOK
		if s.frozen {
			want = StateFrozen
		} else if s.available < 100*mib+100*mib { // reserve + max(reserve, 10% of total)
			want = StateWarn
		}
		if got := m.Snapshot().State; got != want {
			t.Fatalf("%s: state %q, want %q", s.why, got, want)
		}
	}
}

// TestTheTightestFileSystemDecides: when the directories Atlas writes to sit on more
// than one file system, the one with the least room decides, and each file system is
// reported once with every directory on it.
func TestTheTightestFileSystemDecides(t *testing.T) {
	d := &disk{
		volumeOf: map[string]string{"/data": "root", "/data/wal": "fast", "/data/state": "root"},
		usage:    map[string]diskspace.Usage{},
	}
	c := newClock()
	m := monitorOver(d, c, 100*mib)
	d.set("root", 10000*mib, 9000*mib)
	d.set("fast", 1000*mib, 50*mib)
	m.Sample()

	if !m.Frozen() {
		t.Fatal("a full WAL disk did not freeze the server because the data disk had room")
	}
	snap := m.Snapshot()
	if len(snap.FileSystems) != 2 {
		t.Fatalf("reported %d file systems, want 2: %+v", len(snap.FileSystems), snap.FileSystems)
	}
	byVolume := map[string]FileSystem{}
	for _, fs := range snap.FileSystems {
		byVolume[fs.paths()] = fs
	}
	if _, ok := byVolume["/data /data/state"]; !ok {
		t.Fatalf("the data disk is not reported with both of its directories: %+v", snap.FileSystems)
	}
	if fs, ok := byVolume["/data/wal"]; !ok || !fs.Decides {
		t.Fatalf("the WAL disk is not reported as the one that decides: %+v", snap.FileSystems)
	}
}

// TestAnUnreadableFileSystemChangesNothing: a reading that fails cannot say there is
// room, so a frozen server stays frozen; nor can it say there is none, so an open one
// stays open. Either way the snapshot says the reading failed.
func TestAnUnreadableFileSystemChangesNothing(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)

	d.set("v1", 1000*mib, 500*mib)
	m.Sample()
	d.err = errors.New("statfs: input/output error")
	c.advance(5 * time.Second)
	m.Sample()
	if m.Frozen() {
		t.Fatal("an unreadable disk froze an open server")
	}
	if s := m.Snapshot(); s.State != StateUnknown || s.Error == "" {
		t.Fatalf("snapshot = %+v, want state unknown with the error", s)
	}

	d.err = nil
	d.set("v1", 1000*mib, 10*mib)
	c.advance(5 * time.Second)
	m.Sample()
	d.err = errors.New("statfs: input/output error")
	c.advance(5 * time.Second)
	m.Sample()
	if !m.Frozen() {
		t.Fatal("an unreadable disk thawed a frozen server")
	}
}

// TestAZeroReserveNeverFreezes: an operator who turned the freeze off still sees the
// disk, and is warned, but nothing is refused.
func TestAZeroReserveNeverFreezes(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 0)
	d.set("v1", 1000*mib, 0)
	m.Sample()
	if m.Frozen() {
		t.Fatal("a zero reserve froze the server")
	}
	if s := m.Snapshot(); s.State != StateWarn {
		t.Fatalf("a full disk with the freeze off reads %q, want warn", s.State)
	}
}

// TestAHaltedEngineIsTheStateThatMatters: once the engine has halted after a failed
// write, that is what the operator must hear first — it needs a restart, not space.
func TestAHaltedEngineIsTheStateThatMatters(t *testing.T) {
	d, c := oneDisk(), newClock()
	halted := error(nil)
	m := New(Config{
		Dirs:    []string{"/data"},
		Reserve: 100 * mib,
		Read:    d.read,
		Now:     c.now,
		Halted:  func() error { return halted },
	})
	d.set("v1", 1000*mib, 500*mib)
	m.Sample()
	if s := m.Snapshot(); s.State != StateOK || s.EngineHalted != "" {
		t.Fatalf("snapshot = %+v before the halt", s)
	}
	halted = errors.New("engine halted: write: no space left on device")
	c.advance(5 * time.Second)
	m.Sample()
	if s := m.Snapshot(); s.State != StateHalted || s.EngineHalted == "" {
		t.Fatalf("snapshot = %+v, want state halted with the cause", s)
	}
}

// TestTheForecastIsTheSmoothedConsumption: a steady consumption is measured as such
// once there is enough of it to go on, and the time left is the room above the
// reserve divided by it. Nothing but two numbers per file system is kept for it.
func TestTheForecastIsTheSmoothedConsumption(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)
	available := uint64(10000 * mib)
	const perStep = 1 * mib
	const step = time.Minute

	sample := func() {
		d.set("v1", 20000*mib, available)
		m.Sample()
	}
	sample()
	for i := 0; i < 30; i++ {
		c.advance(step)
		available -= perStep
		sample()
	}
	if f := m.Snapshot().Forecast; f != nil {
		t.Fatalf("a forecast after 30 minutes (%+v); it needs an hour to go on", f)
	}
	for i := 0; i < 90; i++ {
		c.advance(step)
		available -= perStep
		sample()
	}
	f := m.Snapshot().Forecast
	if f == nil {
		t.Fatal("no forecast after two hours of steady consumption")
	}
	wantRate := float64(perStep) / step.Seconds()
	if math.Abs(f.BytesPerSecond-wantRate)/wantRate > 0.01 {
		t.Fatalf("rate %.1f B/s, want %.1f", f.BytesPerSecond, wantRate)
	}
	wantSeconds := float64(available-100*mib) / wantRate
	if f.SecondsUntilFreeze == nil || math.Abs(*f.SecondsUntilFreeze-wantSeconds)/wantSeconds > 0.01 {
		t.Fatalf("seconds until freeze %v, want %.0f", f.SecondsUntilFreeze, wantSeconds)
	}
}

// TestTheForecastSeesThroughCompaction: WAL compaction frees space in steps, so the
// free space is a sawtooth. What matters is the net consumption — the growth less what
// compaction gives back — and that is what the smoothing measures.
func TestTheForecastSeesThroughCompaction(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)
	available := uint64(10000 * mib)
	for i := 0; i <= 240; i++ {
		d.set("v1", 20000*mib, available)
		m.Sample()
		c.advance(time.Minute)
		available -= mib // one MiB a minute of growth
		if i%5 == 4 {
			available += 4 * mib // compaction gives back four every five minutes
		}
	}
	f := m.Snapshot().Forecast
	if f == nil {
		t.Fatal("no forecast after four hours")
	}
	want := 0.2 * mib / 60 // net: one fifth of a MiB a minute
	if math.Abs(f.BytesPerSecond-want)/want > 0.1 {
		t.Fatalf("net rate %.1f B/s, want about %.1f", f.BytesPerSecond, want)
	}
}

// TestNoForecastWhileNothingGrows: a disk that is not filling has no date to give, and
// says so rather than inventing a distant one.
func TestNoForecastWhileNothingGrows(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)
	available := uint64(5000 * mib)
	for i := 0; i <= 90; i++ {
		d.set("v1", 20000*mib, available)
		m.Sample()
		c.advance(time.Minute)
		available += mib // retention is freeing space
	}
	f := m.Snapshot().Forecast
	if f == nil {
		t.Fatal("no forecast at all; want one that reports no growth")
	}
	if f.BytesPerSecond >= 0 || f.SecondsUntilFreeze != nil {
		t.Fatalf("forecast %+v for a shrinking disk, want a negative rate and no date", f)
	}
}

// TestAShortForecastWarns: a disk with plenty of room that will reach the reserve
// within a week is a warning now, not when it gets there.
func TestAShortForecastWarns(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := monitorOver(d, c, 100*mib)
	available := uint64(15000 * mib)
	for i := 0; i <= 90; i++ {
		d.set("v1", 20000*mib, available)
		m.Sample()
		c.advance(time.Minute)
		available -= 2 * mib // ~2.9 GiB a day: the reserve is about five days away
	}
	s := m.Snapshot()
	if s.State != StateWarn {
		t.Fatalf("state %q with the reserve %v away, want warn", s.State, s.Forecast)
	}
}

// TestHandleGetServesTheSnapshot: the route answers what the last reading said, in
// the shape the Console and the MCP tool read.
func TestHandleGetServesTheSnapshot(t *testing.T) {
	d, c := oneDisk(), newClock()
	m := New(Config{
		Dirs:    []string{"/data", "/data/wal", "/data/state"},
		Reserve: 100 * mib,
		Read:    d.read,
		Now:     c.now,
		Footprint: func() Footprint {
			return Footprint{WALBytes: 3 * mib, WALSegments: 2, StateBytes: 7 * mib}
		},
	})
	d.set("v1", 1000*mib, 400*mib)
	m.Sample()

	rec := httptest.NewRecorder()
	m.HandleGet(rec, httptest.NewRequest(http.MethodGet, "/api/v1/storage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"state", "reason", "sampledAt", "reserveBytes", "resumeBytes", "fileSystems", "atlas"} {
		if _, ok := got[key]; !ok {
			t.Errorf("response lacks %q: %s", key, rec.Body)
		}
	}
	fss := got["fileSystems"].([]any)
	fs := fss[0].(map[string]any)
	if fs["availableBytes"].(float64) != 400*mib || fs["usedPercent"].(float64) != 60 {
		t.Fatalf("file system %v, want 400 MiB available and 60%% used", fs)
	}
	atlas := got["atlas"].(map[string]any)
	if atlas["walBytes"].(float64) != 3*mib || atlas["stateBytes"].(float64) != 7*mib {
		t.Fatalf("atlas footprint %v", atlas)
	}
}

// TestBeforeTheFirstReading: nothing has been read yet, so nothing is claimed.
func TestBeforeTheFirstReading(t *testing.T) {
	m := monitorOver(oneDisk(), newClock(), 100*mib)
	if m.Frozen() {
		t.Fatal("frozen before any reading")
	}
	if s := m.Snapshot(); s.State != StateUnknown {
		t.Fatalf("state %q before any reading, want unknown", s.State)
	}
}

// TestRunSamplesUntilQuit: the loop takes a reading at once, so the dashboard has
// one from the start, and stops when told.
func TestRunSamplesUntilQuit(t *testing.T) {
	d, c := oneDisk(), newClock()
	d.set("v1", 1000*mib, 10*mib)
	m := monitorOver(d, c, 100*mib)
	quit := make(chan struct{})
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		m.run(quit, ticks)
		close(done)
	}()
	ticks <- time.Time{}
	close(quit)
	<-done
	if !m.Frozen() {
		t.Fatal("the loop took no reading")
	}
}

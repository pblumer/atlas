// Package storage watches the disk under the data directory and keeps the server from
// running it full (ADR-0481).
//
// A full disk is where an event-sourced engine is most fragile. The WAL can be
// repaired — a write it could not finish is cut off again — but the state store
// cannot: Pebble ends the process when it fails to write its manifest, and a
// checkpoint, which is how WAL space is given back, needs room of its own to be
// taken. So the guard is a reserve, not a reaction. While less than the reserve is
// available, the server takes no new writes (the HTTP layer answers 507) and starts no
// work of its own; what is already running may finish, and reads go on. Once the
// reserve and a margin above it are free again, it resumes by itself, with nothing to
// restart.
//
// The [Monitor] reads the file system on a fixed cadence, off the run loop: a reading
// is a single statfs or GetDiskFreeSpaceExW call whose cost does not depend on the
// data (ADR-0382). It also forecasts when the reserve will be reached, without
// keeping a history: each file system carries two numbers, a smoothed consumption
// rate and how long it has been observed. Prometheus users get the same readings as
// gauges and can ask predict_linear for a forecast over as long a window as their
// retention holds.
package storage

import (
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/internal/diskspace"
	"github.com/pblumer/atlas/logging"
)

// DefaultReserve is the room the server keeps free for itself: 1 GiB.
//
// It is sized from what still has to fit after the server stops taking writes. A
// state-store flush writes out its two 16 MiB memtables; a compaction writes its
// output before deleting its input; the manifest Pebble rewrites can reach 128 MiB;
// a checkpoint copies that manifest and Pebble's log beside the files it links; the
// WAL may roll a fresh segment; and the requests already admitted when the reading
// was taken still land. That adds up to roughly half a gibibyte. The default is twice
// that, because the cost of an overestimate is a server that pauses early and the
// cost of an underestimate is Pebble ending the process. It is an estimate from those
// sizes, not a measurement.
const DefaultReserve uint64 = 1 << 30

// DefaultInterval is how often the disk is read.
const DefaultInterval = 5 * time.Second

const (
	// smoothing is the time constant of the consumption estimate: a reading this old
	// counts a third as much as one taken now. Six hours averages a working day's
	// bursts and compaction's sawtooth into one rate while still following a change
	// in load within the day.
	smoothing = 6 * time.Hour
	// warmUp is how much observation a forecast needs before it is shown. Before
	// that the estimate is a few minutes' noise.
	warmUp = time.Hour
	// warnHorizon is how soon a forecast reaching the reserve is a warning.
	warnHorizon = 7 * 24 * time.Hour
)

// State is what the monitor concludes about the disk.
type State string

const (
	// StateOK is plenty of room and no forecast of trouble within a week.
	StateOK State = "ok"
	// StateWarn is room running low, or a forecast that reaches the reserve within a
	// week. Nothing is refused yet.
	StateWarn State = "warn"
	// StateFrozen is less than the reserve available: new writes are refused.
	StateFrozen State = "frozen"
	// StateHalted is the engine stopped after a batch could not be made durable. It
	// needs a restart, which is why it outranks every other state.
	StateHalted State = "halted"
	// StateUnknown is no usable reading: none taken yet, or the last one failed.
	StateUnknown State = "unknown"
)

// Footprint is what Atlas itself holds on disk, as far as it can tell without walking
// a directory: the WAL's segment sizes and the state store's own account.
type Footprint struct {
	WALBytes    int64  `json:"walBytes"`
	WALSegments int    `json:"walSegments"`
	StateBytes  uint64 `json:"stateBytes"`
}

// Forecast is the consumption the monitor has measured and where it leads.
type Forecast struct {
	// BytesPerSecond is the smoothed net consumption: positive while the disk fills,
	// negative while something — retention, compaction — gives back more than is
	// written.
	BytesPerSecond float64 `json:"bytesPerSecond"`
	BytesPerDay    float64 `json:"bytesPerDay"`
	// SecondsUntilFreeze is how long until less than the reserve is available at
	// that rate; absent while nothing grows.
	SecondsUntilFreeze *float64 `json:"secondsUntilFreeze,omitempty"`
	// BasisSeconds is how much observation the rate rests on. It starts over when the
	// server does: nothing of it is stored.
	BasisSeconds float64 `json:"basisSeconds"`
}

// FileSystem is one file system the server writes to.
type FileSystem struct {
	// Paths are the directories of the server's that live on it.
	Paths          []string `json:"paths"`
	TotalBytes     uint64   `json:"totalBytes"`
	UsedBytes      uint64   `json:"usedBytes"`
	AvailableBytes uint64   `json:"availableBytes"`
	// UsedPercent is df's Use%: used / (used + available), to one decimal.
	UsedPercent float64 `json:"usedPercent"`
	// WarnBelowBytes is the available space under which this file system warns: the
	// reserve plus the larger of the reserve and a tenth of used + available.
	WarnBelowBytes uint64 `json:"warnBelowBytes"`
	// Decides marks the file system with the least room — the one that freezes the
	// server first.
	Decides  bool      `json:"decides"`
	Forecast *Forecast `json:"forecast,omitempty"`

	volume string
}

func (fs FileSystem) paths() string { return strings.Join(fs.Paths, " ") }

// Snapshot is the monitor's last conclusion, as GET /api/v1/storage serves it.
type Snapshot struct {
	State  State  `json:"state"`
	Reason string `json:"reason"`
	// SampledAt is when the file systems were last read successfully; null before the
	// first reading.
	SampledAt    *time.Time   `json:"sampledAt"`
	ReserveBytes uint64       `json:"reserveBytes"`
	ResumeBytes  uint64       `json:"resumeBytes"`
	FileSystems  []FileSystem `json:"fileSystems"`
	// Forecast is the soonest any file system reaches the reserve.
	Forecast     *Forecast `json:"forecast,omitempty"`
	Atlas        Footprint `json:"atlas"`
	EngineHalted string    `json:"engineHalted,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Config is what a Monitor is built from. Only Dirs is required.
type Config struct {
	// Dirs are the directories the server writes to. Several on one file system are
	// read as one.
	Dirs []string
	// Reserve is the space kept free; 0 turns the freeze off and leaves the warnings.
	Reserve uint64
	// Read reads a file system; diskspace.Of unless a test supplies one.
	Read func(dir string) (diskspace.Usage, error)
	// Now is the clock readings are timed by; time.Now unless a test supplies one.
	Now func() time.Time
	// Halted reports why the engine stopped, or nil while it runs.
	Halted func() error
	// Footprint reports what Atlas holds on disk. Called on every reading, off the
	// run loop, so it must be safe from any goroutine.
	Footprint func() Footprint
}

// Monitor reads the disk and decides whether the server may take writes.
type Monitor struct {
	cfg    Config
	resume uint64

	// frozen is read on every write request, so it is an atomic rather than behind
	// the mutex the readings take.
	frozen atomic.Bool

	mu        sync.Mutex
	snap      Snapshot
	trends    map[string]*trend
	lastState State
	sawHalt   bool
}

// New builds a monitor. It reads nothing until Sample or Run is called.
func New(cfg Config) *Monitor {
	if cfg.Read == nil {
		cfg.Read = diskspace.Of
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	m := &Monitor{
		cfg: cfg,
		// A quarter of the reserve above it before writes resume, so a disk hovering
		// at the line does not open and close the server on every reading.
		resume: cfg.Reserve + cfg.Reserve/4,
		trends: map[string]*trend{},
	}
	m.snap = Snapshot{
		State:        StateUnknown,
		Reason:       "the disk has not been read yet",
		ReserveBytes: cfg.Reserve,
		ResumeBytes:  m.resume,
		FileSystems:  []FileSystem{},
	}
	m.lastState = StateUnknown
	return m
}

// Frozen reports whether new writes are refused. Cheap enough for every request.
func (m *Monitor) Frozen() bool { return m.frozen.Load() }

// Snapshot returns the last conclusion. The caller owns the copy.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.snap
	s.FileSystems = make([]FileSystem, len(m.snap.FileSystems))
	for i, fs := range m.snap.FileSystems {
		fs.Paths = append([]string(nil), fs.Paths...)
		s.FileSystems[i] = fs
	}
	return s
}

// Run reads the disk every interval until quit is closed, starting at once so the
// first request already has a reading.
func (m *Monitor) Run(quit <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	m.run(quit, t.C)
}

func (m *Monitor) run(quit <-chan struct{}, ticks <-chan time.Time) {
	m.Sample()
	for {
		select {
		case <-quit:
			return
		case <-ticks:
			m.Sample()
		}
	}
}

// HandleGet serves GET /api/v1/storage: the last reading, what it means, and the
// forecast. It reads nothing itself — the monitor's loop does — so it costs the same
// however full the disk is.
func (m *Monitor) HandleGet(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, m.Snapshot())
}

// Sample reads every file system once and updates the conclusion.
func (m *Monitor) Sample() {
	now := m.cfg.Now()
	var halted error
	if m.cfg.Halted != nil {
		halted = m.cfg.Halted()
	}
	var footprint Footprint
	if m.cfg.Footprint != nil {
		footprint = m.cfg.Footprint()
	}
	fss, readErr := m.read()

	m.mu.Lock()
	defer m.mu.Unlock()

	if halted != nil && !m.sawHalt {
		m.sawHalt = true
		logging.Error(logging.EngineHalted,
			"the engine has halted: a batch could not be made durable; free space if the disk is full, then restart the server",
			slog.String("error", halted.Error()))
	}

	s := m.snap
	s.Atlas = footprint
	s.EngineHalted = ""
	if halted != nil {
		s.EngineHalted = halted.Error()
	}
	if readErr != nil {
		// The last good file systems stay on show, dated by SampledAt; the reading
		// that failed changes neither the freeze nor the forecasts.
		s.Error = readErr.Error()
		s.State, s.Reason = StateUnknown, "the disk could not be read: "+readErr.Error()
		if m.frozen.Load() {
			s.Reason += "; the server stays frozen until a reading shows room again"
		}
		if halted != nil {
			s.State, s.Reason = StateHalted, haltReason
		}
		if m.lastState != StateUnknown {
			logging.Warn(logging.StorageUnreadable, "the disk under the data directory could not be read",
				slog.String("error", readErr.Error()))
		}
		m.lastState = StateUnknown
		m.snap = s
		return
	}

	seen := map[string]bool{}
	tightest := -1
	for i := range fss {
		fs := &fss[i]
		seen[fs.volume] = true
		tr := m.trends[fs.volume]
		if tr == nil {
			tr = &trend{}
			m.trends[fs.volume] = tr
		}
		tr.observe(now, fs.AvailableBytes)
		fs.Forecast = tr.forecast(fs.AvailableBytes, m.cfg.Reserve)
		// A tenth of what this server can ever have — used plus available, as the
		// share in use is counted — not of Total: on a disk with a large reservation
		// for root, a tenth of Total can exceed everything this process may write.
		fs.WarnBelowBytes = m.cfg.Reserve + max(m.cfg.Reserve, (fs.UsedBytes+fs.AvailableBytes)/10)
		if tightest < 0 || fs.AvailableBytes < fss[tightest].AvailableBytes {
			tightest = i
		}
	}
	for v := range m.trends {
		if !seen[v] {
			delete(m.trends, v) // a file system no longer mounted there starts over
		}
	}
	if tightest >= 0 {
		fss[tightest].Decides = true
	}

	m.decideFreeze(fss, tightest)

	s.SampledAt = &now
	s.Error = ""
	s.FileSystems = fss
	s.Forecast = soonest(fss, tightest)
	s.State, s.Reason = m.conclude(fss, halted)
	if s.State == StateWarn && (m.lastState == StateOK || m.lastState == StateUnknown) {
		logging.Warn(logging.StorageLow, "the disk under the data directory is running low: "+s.Reason)
	}
	m.lastState = s.State
	m.snap = s
}

const haltReason = "the engine halted after a write to disk failed and processes nothing more; free space if the disk is full, then restart the server"

// read takes one reading of every directory and folds the directories that share a
// file system into one entry, in the order the directories were given.
func (m *Monitor) read() ([]FileSystem, error) {
	var fss []FileSystem
	index := map[string]int{}
	for _, dir := range m.cfg.Dirs {
		u, err := m.cfg.Read(dir)
		if err != nil {
			return nil, err
		}
		if i, ok := index[u.Volume]; ok {
			fss[i].Paths = append(fss[i].Paths, dir)
			continue
		}
		index[u.Volume] = len(fss)
		fss = append(fss, FileSystem{
			Paths:          []string{dir},
			TotalBytes:     u.Total,
			UsedBytes:      u.Used(),
			AvailableBytes: u.Available,
			UsedPercent:    math.Round(u.UsedFraction()*1000) / 10,
			volume:         u.Volume,
		})
	}
	return fss, nil
}

// decideFreeze freezes below the reserve and thaws above the resume line. Called with
// the mutex held.
func (m *Monitor) decideFreeze(fss []FileSystem, tightest int) {
	if m.cfg.Reserve == 0 || tightest < 0 {
		if m.frozen.Swap(false) {
			logging.Info(logging.StorageResumed, "the storage freeze is off; the server takes writes again")
		}
		return
	}
	fs := fss[tightest]
	switch {
	case !m.frozen.Load() && fs.AvailableBytes < m.cfg.Reserve:
		m.frozen.Store(true)
		logging.Error(logging.StorageFrozen,
			"less than the storage reserve is available; the server refuses new writes until there is room again",
			slog.String("paths", fs.paths()),
			slog.Uint64("availableBytes", fs.AvailableBytes),
			slog.Uint64("reserveBytes", m.cfg.Reserve))
	case m.frozen.Load() && fs.AvailableBytes >= m.resume:
		m.frozen.Store(false)
		logging.Info(logging.StorageResumed, "the storage reserve is free again; the server takes writes again",
			slog.String("paths", fs.paths()),
			slog.Uint64("availableBytes", fs.AvailableBytes))
	}
}

// conclude names the state and says why, the most urgent first. Called with the
// mutex held, after decideFreeze.
func (m *Monitor) conclude(fss []FileSystem, halted error) (State, string) {
	if halted != nil {
		return StateHalted, haltReason
	}
	if m.frozen.Load() {
		return StateFrozen, "less than the reserve is available, so the server refuses new writes; it takes them again by itself once the reserve and a quarter of it are free"
	}
	for _, fs := range fss {
		if fs.AvailableBytes < fs.WarnBelowBytes {
			return StateWarn, "little room is left above the reserve on " + fs.paths()
		}
	}
	for _, fs := range fss {
		if f := fs.Forecast; f != nil && f.SecondsUntilFreeze != nil && *f.SecondsUntilFreeze < warnHorizon.Seconds() {
			return StateWarn, "at the current consumption the reserve on " + fs.paths() + " is reached within a week"
		}
	}
	return StateOK, "enough room, and no forecast of the reserve being reached within a week"
}

// soonest is the forecast that reaches the reserve first, or the deciding file
// system's while none reaches it at all.
func soonest(fss []FileSystem, tightest int) *Forecast {
	var best *Forecast
	for _, fs := range fss {
		f := fs.Forecast
		if f == nil || f.SecondsUntilFreeze == nil {
			continue
		}
		if best == nil || *f.SecondsUntilFreeze < *best.SecondsUntilFreeze {
			best = f
		}
	}
	if best == nil && tightest >= 0 {
		best = fss[tightest].Forecast
	}
	return best
}

// trend is the consumption estimate of one file system: an exponentially weighted
// mean of the rate at which available space shrinks, and how long it has been
// watched. Those two numbers are the whole history; nothing else is kept.
//
// The mean starts from zero and is divided by the weight it has accumulated, so the
// first hour is an ordinary average of that hour rather than the first reading's
// noise carried forward, and later readings fade with the smoothing constant.
type trend struct {
	at       time.Time
	avail    float64
	rate     float64 // bytes per second, positive while the disk fills; not yet de-biased
	observed float64 // seconds watched
	started  bool
}

func (t *trend) observe(at time.Time, available uint64) {
	if !t.started {
		t.at, t.avail, t.started = at, float64(available), true
		return
	}
	dt := at.Sub(t.at).Seconds()
	if dt <= 0 {
		return
	}
	r := (t.avail - float64(available)) / dt
	alpha := 1 - math.Exp(-dt/smoothing.Seconds())
	t.rate += alpha * (r - t.rate)
	t.observed += dt
	t.at, t.avail = at, float64(available)
}

func (t *trend) forecast(available, reserve uint64) *Forecast {
	if t.observed < warmUp.Seconds() {
		return nil
	}
	rate := t.rate / (1 - math.Exp(-t.observed/smoothing.Seconds()))
	f := &Forecast{BytesPerSecond: rate, BytesPerDay: rate * 86400, BasisSeconds: t.observed}
	if rate > 0 {
		left := 0.0
		if available > reserve {
			left = float64(available - reserve)
		}
		secs := left / rate
		f.SecondsUntilFreeze = &secs
	}
	return f
}

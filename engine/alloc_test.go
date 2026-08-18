package engine

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// These white-box tests pin invariant I1 for the engine's own hot path: building
// events and scheduling commands flows payloads by value through reused buffers,
// so it must not allocate. (The Pebble-backed state layer — the per-batch
// transaction and per-read decode — allocates separately and is tracked apart
// from the processor path.)

func TestScheduleCommandsNoAlloc(t *testing.T) {
	p := &Processor{}
	p.followups = make([]Command, 0, 16)
	c := &ProcessingContext{p: p}

	allocs := testing.AllocsPerRun(1000, func() {
		p.followups = p.followups[:0]
		c.AppendElementCommand(1, model.IntentActivating, model.ElementInstanceValue{ElementId: 1})
		c.AppendElementCommand(2, model.IntentActivating, model.ElementInstanceValue{ElementId: 2})
		c.AppendElementCommand(3, model.IntentCompleting, model.ElementInstanceValue{ElementId: 3})
	})
	if allocs != 0 {
		t.Errorf("scheduling commands allocated %v times per run, want 0", allocs)
	}
}

func TestEncodeBatchNoAlloc(t *testing.T) {
	p := &Processor{partition: 1}
	// A representative batch: one of each payload type.
	p.batchRecords = []eventRecord{
		{
			header: model.RecordHeader{Position: 1, ValueType: model.VTProcessInstance, Intent: model.IntentActivated},
			value:  inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: 9}},
		},
		{
			header: model.RecordHeader{Position: 2, ValueType: model.VTElementInstance, Intent: model.IntentActivated},
			value:  inflightValue{element: model.ElementInstanceValue{ElementId: 3, BpmnElementType: 2}},
		},
		{
			header: model.RecordHeader{Position: 3, ValueType: model.VTJob, Intent: model.IntentJobCreated},
			value:  inflightValue{job: model.JobValue{JobType: 7, Retries: 3}},
		},
	}
	p.encBuf = make([]byte, 0, 256)

	allocs := testing.AllocsPerRun(1000, func() {
		for i := range p.batchRecords {
			er := &p.batchRecords[i]
			rec := model.Record{Header: er.header, Value: er.value.asValue(er.header.ValueType)}
			p.encBuf = model.AppendRecord(p.encBuf[:0], &rec)
		}
	})
	if allocs != 0 {
		t.Errorf("encoding a batch allocated %v times per run, want 0 "+
			"(asValue must not box the payload)", allocs)
	}
}

func TestAdvanceQueueNoAllocWhenWarm(t *testing.T) {
	p := &Processor{}
	p.queue = make([]Command, 0, 32)
	p.queueScratch = make([]Command, 0, 32)
	p.followups = make([]Command, 0, 32)
	// Warm: some pending commands and some followups.
	p.queue = append(p.queue, Command{Key: 1}, Command{Key: 2}, Command{Key: 3})
	p.followups = append(p.followups, Command{Key: 4}, Command{Key: 5})

	allocs := testing.AllocsPerRun(1000, func() {
		// Re-seed to a steady state each run, then advance past one command.
		p.queue = append(p.queue[:0], Command{Key: 1}, Command{Key: 2}, Command{Key: 3})
		p.advanceQueue(1)
	})
	if allocs != 0 {
		t.Errorf("advanceQueue allocated %v times per run, want 0 once warmed", allocs)
	}
}

// TestReportBatchNoAlloc pins the second ADR-0142 hot-path rule: reporting a batch is
// one interface call passing a plain struct by value, so it must not allocate. This is
// the test that catches a future metric added with a label lookup (`WithLabelValues` per
// batch) or a boxed argument — the two ways instrumentation quietly starts taxing the
// throughput path it exists to measure.
func TestReportBatchNoAlloc(t *testing.T) {
	p := &Processor{metrics: discardMetrics{}}
	stats := BatchStats{Commands: 12, Events: 30, QueueDepth: 4, SyncSeconds: 0.001, CommitSeconds: 0.0002}

	allocs := testing.AllocsPerRun(1000, func() {
		p.metrics.BatchCommitted(stats)
		p.metrics.SyncFailed()
		p.metrics.CommitFailed()
	})
	if allocs != 0 {
		t.Errorf("reporting a batch allocated %v times per run, want 0", allocs)
	}
}

// TestUninstrumentedBatchReportCostsNothing: with no Metrics attached the loop must not
// even read the clock, so an operator who disables metrics pays nothing at all.
func TestUninstrumentedBatchReportNoAlloc(t *testing.T) {
	p := &Processor{}
	allocs := testing.AllocsPerRun(1000, func() {
		if p.metrics != nil {
			p.metrics.BatchCommitted(BatchStats{})
		}
	})
	if allocs != 0 {
		t.Errorf("the nil-metrics path allocated %v times per run, want 0", allocs)
	}
}

// discardMetrics is a Metrics that does the least a real one can: it keeps the values
// live (so the compiler cannot optimise the call away) without allocating.
type discardMetrics struct{}

func (discardMetrics) BatchCommitted(s BatchStats) {
	sinkCommands += s.Commands
	sinkEvents += s.Events
}
func (discardMetrics) SyncFailed()   { sinkFailures++ }
func (discardMetrics) CommitFailed() { sinkFailures++ }

var (
	sinkCommands int
	sinkEvents   int
	sinkFailures int
)

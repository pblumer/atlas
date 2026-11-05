package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

const deviceBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="giessen" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="ein">
      <bpmn:extensionElements><atlas:deviceConnector connector="pi" operation="digital.write" output="relais" on="=bewaessern"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="lesen">
      <bpmn:extensionElements><atlas:deviceConnector connector="pi" operation="digital.read" line="relais" resultVariable="zustand"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="ein"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ein" targetRef="lesen"/>
    <bpmn:sequenceFlow id="f3" sourceRef="lesen" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func deviceEnv(extra map[string]string) func(string) string {
	env := map[string]string{
		"ATLAS_DEVICE_CONNECTORS": "pi",
		"ATLAS_DEVICE_PI_CONFIG":  `{"chip":"gpiochip0","outputs":[{"name":"relais","line":17,"activeLow":true}]}`,
		"ATLAS_DEVICE_MOCK":       "1",
	}
	for k, v := range extra {
		env[k] = v
	}
	return func(name string) string { return env[name] }
}

// A model switches an output and reads it back through a real server: the task is
// compiled at deploy, its FEEL resolved by the server when the device worker leases the
// job, and the worker on the "device" — here a chip in memory — drives the line.
func TestADeviceTaskRunsEndToEnd(t *testing.T) {
	ts := liveAtlasWith(t, deviceBPMN, `{"bewaessern":true}`)
	built, err := worker.BuiltinConnectors(deviceEnv(nil), "device")
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if len(built.Names) != 1 || built.Names[0] != "pi" || built.OfflineAfter != worker.DefaultDeviceOfflineAfter {
		t.Fatalf("names %v, offline after %s", built.Names, built.OfflineAfter)
	}
	w := worker.New(worker.Options{Server: ts.URL, ID: "pi-1", Handlers: built.Handlers, Connectors: built.Names, Wait: time.Millisecond})
	for i := 0; i < 2; i++ {
		if err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := runningInstances(t, ts); n != 0 {
		t.Fatalf("%d instances still running; a device task did not complete", n)
	}
	vars := instanceVariables(t, ts)
	zustand, _ := vars["zustand"].(map[string]any)
	if zustand["on"] != true {
		t.Fatalf("zustand = %#v, want the relay read back as on", vars["zustand"])
	}
}

func TestADeviceWorkerWithoutDevicesServesNothing(t *testing.T) {
	built, err := worker.BuiltinConnectors(func(string) string { return "" }, "device")
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Handlers) != 0 || len(built.Unconfigured) != 1 || built.Unconfigured[0] != "device" {
		t.Fatalf("handlers %d, unconfigured %v", len(built.Handlers), built.Unconfigured)
	}
	if err := built.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestADeviceWorkerDrivesItsOutputsSafeAndReleasesThem(t *testing.T) {
	built, err := worker.BuiltinConnectors(deviceEnv(map[string]string{"ATLAS_DEVICE_OFFLINE_AFTER": "30s"}), "device")
	if err != nil {
		t.Fatal(err)
	}
	if built.OfflineAfter != 30*time.Second {
		t.Fatalf("offline after %s", built.OfflineAfter)
	}
	if err := built.MakeSafe(); err != nil {
		t.Fatal(err)
	}
	if err := built.Close(); err != nil {
		t.Fatal(err)
	}
	if err := built.MakeSafe(); err == nil {
		t.Fatal("a closed device was driven")
	}
}

// A job the device refuses fails with no retries left, so an operator reads an
// incident at once instead of the worker spending the budget on the same answer.
func TestAPermanentFailureSpendsEveryRetry(t *testing.T) {
	var (
		mu       sync.Mutex
		failBody map[string]any
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activate"):
			_, _ = w.Write([]byte(`{"jobs":[{"jobKey":7,"type":"io.atlas.device","retries":3,"leaseToken":42,
				"connector":{"kind":"device","fields":{"connector":"pi","operation":"digital.write","fields":{"output":"pumpe","on":true}}}}]}`))
		case strings.HasSuffix(r.URL.Path, "/fail"):
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewDecoder(r.Body).Decode(&failBody)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected call to %s", r.URL.Path)
		}
	}))
	defer stub.Close()
	built, err := worker.BuiltinConnectors(deviceEnv(nil), "device")
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	w := worker.New(worker.Options{Server: stub.URL, ID: "pi-1", Handlers: built.Handlers})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if failBody["retries"] != float64(0) {
		t.Fatalf("retries = %v, want 0 for a job the device refuses", failBody["retries"])
	}
	if msg, _ := failBody["message"].(string); !strings.Contains(msg, "pumpe") {
		t.Fatalf("message = %q", msg)
	}
}

func TestPermanentWrapsAndUnwraps(t *testing.T) {
	if worker.Permanent(nil) != nil {
		t.Fatal("no error is no permanent error")
	}
	base := errors.New("refused")
	err := worker.Permanent(base)
	if !errors.Is(err, base) || err.Error() != "refused" {
		t.Fatalf("err = %v", err)
	}
}

// A worker whose server stops answering calls OnOffline once the polls have failed for
// OfflineAfter, and OnOnline on the first poll that succeeds again.
func TestAWorkerNoticesItsServerIsGoneAndBack(t *testing.T) {
	var (
		mu    sync.Mutex
		down  = true
		polls int
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		polls++
		if down {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	}))
	defer stub.Close()

	var (
		clockMu sync.Mutex
		now     = time.Unix(0, 0)
	)
	offline, online := make(chan struct{}, 4), make(chan struct{}, 4)
	w := worker.New(worker.Options{
		Server: stub.URL, ID: "pi-1", Retry: time.Millisecond, Wait: time.Millisecond,
		Handlers:     map[string]worker.Exec{"io.atlas.device": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) { return nil, nil })},
		OfflineAfter: time.Minute,
		OnOffline:    func() { offline <- struct{}{} },
		OnOnline:     func() { online <- struct{}{} },
		// Each poll moves the clock on by twenty seconds, so the fourth failure is past
		// the minute without the test waiting one.
		Now: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			now = now.Add(20 * time.Second)
			return now
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-offline:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never noticed its server was gone")
	}
	mu.Lock()
	down = false
	mu.Unlock()
	select {
	case <-online:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never noticed its server was back")
	}
	cancel()
	<-done
	if len(offline) != 0 {
		t.Fatal("OnOffline was called more than once for one outage")
	}
}

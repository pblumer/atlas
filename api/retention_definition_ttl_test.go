package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// These tests exercise the per-definition history TTL (atlas:historyTtl, ADR-0144):
// a definition states how long its own finished instances are kept, overriding — or
// standing in for — the server-wide --retention-max-age. They reuse the deterministic
// retention harness (shared fake clock, explicit sweep trigger), so eligibility is
// decided by advancing the clock, never by wall-clock timing.

// ttlRetentionBPMN parks at a service task like retentionBPMN, but its process declares
// a 30-minute history TTL. A different process id, so both can be deployed side by side.
const ttlRetentionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="quality-check" isExecutable="true" atlas:historyTtl="PT30M">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// deployXML deploys one model and returns the definition key of its first process.
func (h retentionHarness) deployXML(xml string) uint64 {
	h.t.Helper()
	code, body := h.x.do(http.MethodPost, "/api/v1/deployments", xml)
	if code != http.StatusOK {
		h.t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deployed struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deployed); err != nil || deployed.Key == 0 {
		h.t.Fatalf("decode deployment: %v (%s)", err, body)
	}
	return deployed.Key
}

// createParkedOf starts one instance of the given definition — it parks at its service
// task — and returns its key: the highest in the listing, since keys only grow.
func (h retentionHarness) createParkedOf(defKey uint64) uint64 {
	h.t.Helper()
	path := "/api/v1/processes/" + strconv.FormatUint(defKey, 10) + "/instances"
	if code, body := h.x.do(http.MethodPost, path, "{}"); code != http.StatusOK {
		h.t.Fatalf("create status=%d body=%s", code, body)
	}
	var newest uint64
	for _, r := range h.list() {
		if r.Key > newest {
			newest = r.Key
		}
	}
	return newest
}

// has reports whether the instance listing still holds the given key.
func (h retentionHarness) has(key uint64) bool {
	h.t.Helper()
	for _, r := range h.list() {
		if r.Key == key {
			return true
		}
	}
	return false
}

// TestRetentionDefinitionTTLWithoutGlobalMaxAge: with no server-wide max age at all, a
// definition that declares its own historyTtl still self-cleans — the sweep runs for it
// alone — while a definition without one is left untouched (ADR-0144).
func TestRetentionDefinitionTTLWithoutGlobalMaxAge(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	const ttl = 30 * time.Minute
	h := newRetentionHarness(t, t0) // no WithRetention: retention is off server-wide
	ttlKey := h.deployXML(ttlRetentionBPMN)
	plainKey := h.deployXML(retentionBPMN)

	withTTL := h.createParkedOf(ttlKey)
	withoutTTL := h.createParkedOf(plainKey)
	h.terminate(withTTL)
	h.terminate(withoutTTL) // both finish at t0

	// One nanosecond short of the definition's TTL: still ineligible.
	h.clk.set(t0 + int64(ttl) - 1)
	h.sweep()
	if !h.has(withTTL) {
		t.Fatal("instance purged before its definition's history TTL elapsed")
	}

	// At the TTL: the declaring definition's instance goes, the other stays — nothing
	// else on the server has a retention policy.
	h.clk.set(t0 + int64(ttl))
	h.sweep()
	if h.has(withTTL) {
		t.Error("instance past its definition's history TTL was not purged")
	}
	if !h.has(withoutTTL) {
		t.Error("instance of a definition without a history TTL was purged with no server-wide max age")
	}
}

// TestRetentionDefinitionTTLOverridesGlobalMaxAge: a definition's historyTtl wins over
// --retention-max-age for its own instances; every other definition keeps the global
// age (ADR-0144).
func TestRetentionDefinitionTTLOverridesGlobalMaxAge(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	const ttl = 30 * time.Minute
	const globalAge = time.Hour
	h := newRetentionHarness(t, t0, WithRetention(globalAge))
	ttlKey := h.deployXML(ttlRetentionBPMN)
	plainKey := h.deployXML(retentionBPMN)

	withTTL := h.createParkedOf(ttlKey)
	withoutTTL := h.createParkedOf(plainKey)
	h.terminate(withTTL)
	h.terminate(withoutTTL)

	// Half an hour in: the shorter per-definition TTL has elapsed, the global hour has not.
	h.clk.set(t0 + int64(ttl))
	h.sweep()
	if h.has(withTTL) {
		t.Error("definition history TTL did not override the longer server-wide max age")
	}
	if !h.has(withoutTTL) {
		t.Error("instance purged before the server-wide max age elapsed")
	}

	// An hour in: the global age catches the definition that declared no TTL of its own.
	h.clk.set(t0 + int64(globalAge))
	h.sweep()
	if h.has(withoutTTL) {
		t.Error("instance past the server-wide max age was not purged")
	}
}

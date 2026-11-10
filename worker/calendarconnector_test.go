package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/calendar"
)

func calendarEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestTheCalendarRegistryComesFromTheEnvironment(t *testing.T) {
	reg, names, err := calendarRegistryFromEnv(calendarEnv(nil))
	if reg != nil || names != nil || err != nil {
		t.Fatalf("unconfigured: %v %v %v", reg, names, err)
	}
	reg, names, err = calendarRegistryFromEnv(calendarEnv(map[string]string{
		"ATLAS_CALENDAR_CONNECTORS":          "filiale",
		"ATLAS_CALENDAR_FILIALE_CREDENTIALS": `{"provider":"mock","calendars":{"werkstatt":{}}}`,
	}))
	if err != nil || len(names) != 1 {
		t.Fatalf("configured: %v %v", names, err)
	}
	if _, ok := reg.Client("filiale"); !ok {
		t.Fatal("the account is not registered")
	}
	_, _, err = calendarRegistryFromEnv(calendarEnv(map[string]string{
		"ATLAS_CALENDAR_CONNECTORS":          "filiale",
		"ATLAS_CALENDAR_FILIALE_CREDENTIALS": `{"provider":"lotus"}`,
	}))
	if err == nil || !strings.Contains(err.Error(), "ATLAS_CALENDAR_FILIALE_CREDENTIALS") {
		t.Fatalf("a named account that does not build: %v", err)
	}
	built, err := BuiltinConnectors(calendarEnv(nil), "calendar")
	if err != nil || len(built.Unconfigured) != 1 || built.Handlers[compiler.CalendarJobType] != nil {
		t.Fatalf("told to serve calendars holding none: %+v %v", built, err)
	}
	if _, err := BuiltinConnectors(calendarEnv(map[string]string{"ATLAS_CALENDAR_CONNECTORS": "x"}), "calendar"); err == nil {
		t.Fatal("a named account without a bundle was accepted")
	}
}

func TestRunCalendarJobRunsTheResolvedTask(t *testing.T) {
	reg := calendar.NewRegistry()
	reg.Register("filiale", calendar.NewMockCalendars(map[string]calendar.MockCalendarSpec{"werkstatt": {}}))
	start := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Hour)
	out, err := RunCalendarJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "calendar", Fields: map[string]any{
		"connector": "filiale", "operation": "reserve-slot", "calendar": "werkstatt",
		"start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339),
		"resultVariable": "buchung", "createKey": "k", "errorCodes": calendar.Codes,
	}}}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := out["buchung"].(map[string]any); b["bookingId"] == "" || b["calendar"] != "werkstatt" {
		t.Fatalf("result %v", out)
	}
	if _, err := RunCalendarJob(context.Background(), Job{}, reg); err == nil || !strings.HasPrefix(err.Error(), "calendar:") {
		t.Fatalf("a job without a resolved task: %v", err)
	}
	if _, err := RunCalendarJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "calendar", Fields: map[string]any{"connektor": "x"}}}, reg); err == nil {
		t.Fatal("a detail with an unknown field was run")
	}
	if _, err := RunCalendarJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "calendar", Fields: map[string]any{"bad": func() {}}}}, reg); err == nil {
		t.Fatal("a detail that does not encode was run")
	}
}

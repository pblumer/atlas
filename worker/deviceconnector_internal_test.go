package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const piWiring = `{"chip":"gpiochip0","outputs":[{"name":"relais","line":17}]}`

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDevicesFromEnvRefusesAWiringItCannotUse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "device.json")
	if err := os.WriteFile(file, []byte(piWiring), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"no wiring":        {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi"}, "no wiring"},
		"both wirings":     {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG": piWiring, "ATLAS_DEVICE_PI_CONFIG_FILE": file}, "use one"},
		"missing file":     {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG_FILE": file + ".nope"}, "read the wiring"},
		"bad wiring":       {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG": `{"chip":"gpiochip0"}`}, "not usable"},
		"bad offline":      {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_OFFLINE_AFTER": "soon"}, "not a duration"},
		"negative offline": {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_OFFLINE_AFTER": "-1m"}, "not a duration"},
		"no chip here":     {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG": `{"chip":"gpiochip99","outputs":[{"name":"relais","line":17}]}`}, "MOCK=1"},
		"one of two fails": {map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi, pi-sued", "ATLAS_DEVICE_PI_CONFIG": piWiring, "ATLAS_DEVICE_MOCK": "1"}, "pi-sued"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, _, _, err := devicesFromEnv(envOf(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestDevicesFromEnvReadsAFileAndTheOfflineDelay(t *testing.T) {
	file := filepath.Join(t.TempDir(), "device.json")
	if err := os.WriteFile(file, []byte(piWiring), 0o600); err != nil {
		t.Fatal(err)
	}
	devices, names, offline, err := devicesFromEnv(envOf(map[string]string{
		"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG_FILE": file,
		"ATLAS_DEVICE_MOCK": "1", "ATLAS_DEVICE_OFFLINE_AFTER": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer devices["pi"].Close()
	if len(names) != 1 || offline != 0 {
		t.Fatalf("names %v, offline %s", names, offline)
	}
}

func TestRunDeviceJobRefusesWhatItCannotRun(t *testing.T) {
	devices, _, _, err := devicesFromEnv(envOf(map[string]string{"ATLAS_DEVICE_CONNECTORS": "pi", "ATLAS_DEVICE_PI_CONFIG": piWiring, "ATLAS_DEVICE_MOCK": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	defer devices["pi"].Close()
	var pe *permanentError
	if _, err := RunDeviceJob(context.Background(), Job{}, devices); err == nil || !strings.Contains(err.Error(), "no resolved task") || !asPermanent(err, &pe) {
		t.Fatalf("a job without its payload: %v", err)
	}
	bad := Job{Connector: &ConnectorPayload{Kind: "device", Fields: map[string]any{"fields": "not an object"}}}
	if _, err := RunDeviceJob(context.Background(), bad, devices); !asPermanent(err, &pe) {
		t.Fatalf("a payload that does not decode: %v", err)
	}
	unmarshalable := Job{Connector: &ConnectorPayload{Kind: "device", Fields: map[string]any{"x": func() {}}}}
	if _, err := RunDeviceJob(context.Background(), unmarshalable, devices); err == nil {
		t.Fatal("a payload that cannot be encoded ran")
	}
	ok := Job{JobKey: 5, Connector: &ConnectorPayload{Kind: "device", Fields: map[string]any{
		"connector": "pi", "operation": "digital.write", "fields": map[string]any{"output": "relais", "on": true},
	}}}
	if _, err := RunDeviceJob(context.Background(), ok, devices); err != nil {
		t.Fatal(err)
	}
	if on, _ := devices["pi"].Read("relais"); !on {
		t.Fatal("the relay was not switched")
	}
}

func asPermanent(err error, pe **permanentError) bool {
	p, ok := err.(*permanentError)
	*pe = p
	return ok
}

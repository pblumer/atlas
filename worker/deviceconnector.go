package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/device"
)

// deviceEnvPrefix is where a device worker's devices live.
const deviceEnvPrefix = "ATLAS_DEVICE_"

// DefaultDeviceOfflineAfter is how long a device worker may fail to reach its server
// before it drives every output to its safe state. Long enough to ride out a server
// restart, short enough that a lost uplink does not leave a valve open for long. The
// device's own watchdog (maxOnMs) is the guarantee; this is the earlier of the two.
const DefaultDeviceOfflineAfter = 2 * time.Minute

// devicesFromEnv opens the devices this worker holds. ATLAS_DEVICE_CONNECTORS lists the
// names; each name contributes its wiring, as the JSON [device.Config] describes,
// either inline in ATLAS_DEVICE_<NAME>_CONFIG or in the file ATLAS_DEVICE_<NAME>_CONFIG_FILE
// names. ATLAS_DEVICE_MOCK=1 serves every device from a chip in memory instead of the
// GPIO character device, so a model that switches a relay runs end to end on a machine
// with no pins. ATLAS_DEVICE_OFFLINE_AFTER is how long the server may be unreachable
// before the outputs go to their safe state (default 2m; 0 turns it off).
//
// Every line is claimed here, at startup, already in its safe state: a wiring the chip
// cannot satisfy — a line another process holds, a chip that is not there — is an
// error the operator reads now rather than a job that fails later.
func devicesFromEnv(env func(string) string) (map[string]*device.Device, []string, time.Duration, error) {
	names := splitAndTrim(env(deviceEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, 0, nil
	}
	offline := DefaultDeviceOfflineAfter
	if raw := strings.TrimSpace(env(deviceEnvPrefix + "OFFLINE_AFTER")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return nil, nil, 0, fmt.Errorf("worker: %sOFFLINE_AFTER=%q is not a duration such as 2m (0 turns it off)", deviceEnvPrefix, raw)
		}
		offline = d
	}
	mock := env(deviceEnvPrefix+"MOCK") == "1"
	devices := map[string]*device.Device{}
	closeAll := func() {
		for _, d := range devices {
			d.Close()
		}
	}
	for _, name := range names {
		key := deviceEnvPrefix + envFold(name) + "_"
		raw, err := deviceConfig(env, key)
		if err != nil {
			closeAll()
			return nil, nil, 0, fmt.Errorf("worker: device %q: %w", name, err)
		}
		cfg, err := device.ParseConfig(raw)
		if err != nil {
			closeAll()
			return nil, nil, 0, fmt.Errorf("worker: device %q is not usable: %w", name, err)
		}
		var chip device.Chip
		if mock {
			chip = device.NewFakeChip()
		} else if chip, err = device.OpenChip(cfg.Chip); err != nil {
			closeAll()
			return nil, nil, 0, fmt.Errorf("worker: device %q: %w (or set %sMOCK=1 to serve it from memory)", name, err, deviceEnvPrefix)
		}
		d, err := device.Open(cfg, chip, device.SystemClock{})
		if err != nil {
			closeAll()
			return nil, nil, 0, fmt.Errorf("worker: device %q: %w", name, err)
		}
		devices[name] = d
	}
	return devices, names, offline, nil
}

// deviceConfig reads a device's wiring from exactly one of its two variables.
func deviceConfig(env func(string) string, key string) ([]byte, error) {
	inline, file := strings.TrimSpace(env(key+"CONFIG")), strings.TrimSpace(env(key+"CONFIG_FILE"))
	switch {
	case inline != "" && file != "":
		return nil, fmt.Errorf("both %sCONFIG and %sCONFIG_FILE are set; use one", key, key)
	case inline != "":
		return []byte(inline), nil
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read the wiring: %w", err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("no wiring: set %sCONFIG_FILE to a JSON file such as {\"chip\": \"gpiochip0\", \"outputs\": [{\"name\": \"relais\", \"line\": 17}]}, or %sCONFIG to the JSON itself", key, key)
}

// RunDeviceJob performs a resolved device task on the devices this worker holds. A job
// the device refuses — a line it does not have, a value of the wrong type — is returned
// as a [Permanent] failure: no retry can make it right, so it becomes an incident at once.
func RunDeviceJob(ctx context.Context, j Job, devices map[string]*device.Device) (map[string]any, error) {
	if j.Connector == nil {
		return nil, Permanent(errors.New("device: the job carried no resolved task; the server could not evaluate its values (does the worker expression name a device?) or does not resolve device tasks"))
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task device.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, Permanent(fmt.Errorf("device: cannot read the resolved detail: %w", err))
	}
	out, err := device.Run(ctx, task, devices, j.JobKey)
	if device.IsRefused(err) {
		return nil, Permanent(err)
	}
	return out, err
}

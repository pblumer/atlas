package workersdk

import "testing"

func TestLaunchOptionsReadTheLaunchContract(t *testing.T) {
	env := map[string]string{
		EnvServer: "http://127.0.0.1:8080/", EnvWorkerID: "wt-echo", EnvToken: "tok",
		EnvTypeID: "com.example.echo", EnvTypeVersion: "1.0.0", EnvTypeDigest: "sha256:ab",
	}
	opts := LaunchOptions(func(k string) string { return env[k] })
	if opts.Server != "http://127.0.0.1:8080" || opts.ID != "wt-echo" || opts.Token != "tok" {
		t.Errorf("options = %+v", opts)
	}
	if opts.Identity == nil || opts.Identity.ID != "com.example.echo" || opts.Identity.Digest != "sha256:ab" {
		t.Errorf("identity = %+v", opts.Identity)
	}
	if got := LaunchOptions(func(string) string { return "" }); got.Identity != nil {
		t.Errorf("identity with nothing set = %+v, want none", got.Identity)
	}
}

func TestLaunchConfigReadsTheConfiguredWorkers(t *testing.T) {
	env := map[string]string{EnvConfig: `{"workers":[{"name":"main","config":{"greeting":"Hallo"}},{"name":"b","config":{}}]}`}
	workers, err := LaunchConfig(func(k string) string { return env[k] })
	if err != nil || len(workers) != 2 || workers[0].Name != "main" || workers[0].Config["greeting"] != "Hallo" {
		t.Fatalf("LaunchConfig = %+v, %v", workers, err)
	}
	// The instance reports the Workers it holds.
	if opts := LaunchOptions(func(k string) string { return env[k] }); len(opts.Connectors) != 2 || opts.Connectors[1] != "b" {
		t.Errorf("Connectors = %v, want both configured Workers", opts.Connectors)
	}
	if workers, err := LaunchConfig(func(string) string { return "" }); err != nil || workers != nil {
		t.Errorf("nothing configured = %+v, %v; want none and no error", workers, err)
	}
	if _, err := LaunchConfig(func(string) string { return "{" }); err == nil {
		t.Error("a malformed configuration = nil error")
	}
}

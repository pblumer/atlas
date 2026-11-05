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

package device

import (
	"strings"
	"testing"
	"time"
)

const sampleConfig = `{
  "chip": "gpiochip0",
  "outputs": [
    {"name": "relais", "line": 17, "activeLow": true, "maxOnMs": 600000},
    {"name": "luefter", "line": 22, "safe": "on"}
  ],
  "inputs": [
    {"name": "taster", "line": 27, "activeLow": true, "bias": "pull-up"}
  ]
}`

func TestParseConfigReadsAWiring(t *testing.T) {
	c, err := ParseConfig([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Chip != "gpiochip0" || len(c.Outputs) != 2 || len(c.Inputs) != 1 {
		t.Fatalf("parsed %+v", c)
	}
	relais, luefter := c.Outputs[0], c.Outputs[1]
	if relais.SafeOn() || !luefter.SafeOn() {
		t.Fatal("safe defaults to off; luefter is safe on")
	}
	if relais.MaxOn() != 10*time.Minute || luefter.MaxOn() != 0 {
		t.Fatalf("MaxOn: %s, %s", relais.MaxOn(), luefter.MaxOn())
	}
	if c.Inputs[0].bias() != BiasPullUp {
		t.Fatal("taster is pulled up")
	}
}

func TestInputBias(t *testing.T) {
	for raw, want := range map[string]Bias{"": BiasNone, "none": BiasNone, "pull-up": BiasPullUp, "pull-down": BiasPullDown} {
		if got := (Input{Bias: raw}).bias(); got != want {
			t.Errorf("bias %q = %d, want %d", raw, got, want)
		}
	}
}

func TestParseConfigRefuses(t *testing.T) {
	out := func(extra string) string {
		return `{"chip":"gpiochip0","outputs":[{"name":"relais","line":17` + extra + `}]}`
	}
	cases := map[string]struct{ raw, want string }{
		"not json":             {`{`, "configuration"},
		"unknown field":        {`{"chip":"gpiochip0","pins":[]}`, "unknown field"},
		"two values":           {out("") + ` {}`, "more than one"},
		"path as chip":         {`{"chip":"../etc/passwd","outputs":[{"name":"a","line":1}]}`, "not a GPIO"},
		"empty":                {`{"chip":"gpiochip0"}`, "no output"},
		"unknown output field": {out(`,"x":1`), "unknown field"},
		"upper-case name":      {`{"chip":"gpiochip0","outputs":[{"name":"Relais","line":1}]}`, "not a line name"},
		"negative line":        {`{"chip":"gpiochip0","outputs":[{"name":"a","line":-1}]}`, "outside"},
		"huge line":            {`{"chip":"gpiochip0","outputs":[{"name":"a","line":5000}]}`, "outside"},
		"safe":                 {out(`,"safe":"high"`), "want off or on"},
		"negative max-on":      {out(`,"maxOnMs":-1`), "maxOnMs"},
		"max-on over a day":    {out(`,"maxOnMs":86400001`), "maxOnMs"},
		"bias":                 {`{"chip":"gpiochip0","inputs":[{"name":"t","line":2,"bias":"up"}]}`, "bias"},
		"name twice": {`{"chip":"gpiochip0","outputs":[{"name":"a","line":1}],"inputs":[{"name":"a","line":2}]}`,
			"used twice"},
		"line twice": {`{"chip":"gpiochip0","outputs":[{"name":"a","line":1}],"inputs":[{"name":"b","line":1}]}`,
			"both wired to line 1"},
		"bad input name": {`{"chip":"gpiochip0","inputs":[{"name":"T","line":2}]}`, "not a line name"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

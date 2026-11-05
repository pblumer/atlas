package device

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExecuteRefusesARequestBeforeDrivingAnything(t *testing.T) {
	d, chip, _ := openTest(t)
	cases := map[string]struct {
		req  Request
		want string
	}{
		"unknown operation": {Request{Op: "digital.toggle"}, "unknown operation"},
		"unused value":      {Request{Op: "digital.write", Fields: map[string]any{"output": "lampe", "on": true, "durationMs": 5}}, `does not use "durationMs"`},
		"missing value":     {Request{Op: "digital.write", Fields: map[string]any{"output": "lampe"}}, `needs "on"`},
		"name not a string": {Request{Op: "digital.write", Fields: map[string]any{"output": 17, "on": true}}, "output is a name"},
		"state not a bool":  {Request{Op: "digital.write", Fields: map[string]any{"output": "lampe", "on": "yes"}}, "on is true or false"},
		"read of a number":  {Request{Op: "digital.read", Fields: map[string]any{"line": 3}}, "line is a name"},
		"pulse name":        {Request{Op: "pulse", Fields: map[string]any{"output": false, "durationMs": 5}}, "output is a name"},
		"pulse state":       {Request{Op: "pulse", Fields: map[string]any{"output": "lampe", "on": 1, "durationMs": 5}}, "on is true or false"},
		"fractional ms":     {Request{Op: "pulse", Fields: map[string]any{"output": "lampe", "durationMs": 1.5}}, "whole number"},
		"ms as text":        {Request{Op: "pulse", Fields: map[string]any{"output": "lampe", "durationMs": "5"}}, "whole number"},
		"ms as a decimal":   {Request{Op: "pulse", Fields: map[string]any{"output": "lampe", "durationMs": json.Number("2.5")}}, "whole number"},
		"pulse too long":    {Request{Op: "pulse", Fields: map[string]any{"output": "lampe", "durationMs": 120000}}, "timer"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := d.Execute(context.Background(), tc.req)
			if !IsRefused(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal mentioning %q", err, tc.want)
			}
		})
	}
	if len(chip.History()) != 3 {
		t.Fatalf("a refused request drove a line: %v", chip.History())
	}
}

func TestExecuteWriteAndRead(t *testing.T) {
	d, chip, _ := openTest(t)
	ctx := context.Background()
	out, err := d.Execute(ctx, Request{Op: "digital.write", Fields: map[string]any{"output": "lampe", "on": true}})
	if err != nil || out != nil {
		t.Fatalf("write: %v, %v", out, err)
	}
	if !chip.Level(23) {
		t.Fatal("lampe is not on")
	}
	out, err = d.Execute(ctx, Request{Op: "digital.read", Fields: map[string]any{"line": "lampe"}})
	if err != nil || out["on"] != true {
		t.Fatalf("read: %v, %v", out, err)
	}
	if _, err := d.Execute(ctx, Request{Op: "digital.read", Fields: map[string]any{"line": "pumpe"}}); !IsRefused(err) {
		t.Fatalf("read of an unknown line: %v", err)
	}
}

func TestExecutePulseTakesEveryNumberShape(t *testing.T) {
	for label, ms := range map[string]any{"float": float64(250), "json": json.Number("250"), "int": 250, "int64": int64(250)} {
		t.Run(label, func(t *testing.T) {
			d, chip, clock := openTest(t)
			done := make(chan struct{})
			var err error
			go func() {
				_, err = d.Execute(context.Background(), Request{JobKey: 11, Op: "pulse", Fields: map[string]any{"output": "lampe", "durationMs": ms}})
				close(done)
			}()
			clock.awaitArmed(t, 1)
			if !chip.Level(23) {
				t.Fatal("a pulse is on unless told otherwise")
			}
			clock.Advance(250 * time.Millisecond)
			<-done
			if err != nil || chip.Level(23) {
				t.Fatalf("err = %v, level %v", err, chip.Level(23))
			}
		})
	}
}

func TestExecutePulseWithAnExplicitState(t *testing.T) {
	d, chip, clock := openTest(t)
	done := make(chan struct{})
	var err error
	go func() {
		_, err = d.Execute(context.Background(), Request{JobKey: 12, Op: "pulse", Fields: map[string]any{"output": "luefter", "on": false, "durationMs": 100}})
		close(done)
	}()
	clock.awaitArmed(t, 1)
	if chip.Level(22) {
		t.Fatal("luefter is not off during its pulse")
	}
	clock.Advance(100 * time.Millisecond)
	<-done
	if err != nil || !chip.Level(22) {
		t.Fatalf("err = %v; luefter must be back on", err)
	}
}

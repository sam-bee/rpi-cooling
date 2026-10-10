package main

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"
)

type fakeOutput struct {
	states []bool
	setErr error
	closed bool
}

func (f *fakeOutput) Set(on bool) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.states = append(f.states, on)
	return nil
}

func (f *fakeOutput) Close() error {
	f.closed = true
	return nil
}

type temperatureReading struct {
	temp float64
	err  error
}

type fakeSensor struct {
	readings   []temperatureReading
	index      int
	readNotify chan struct{}
}

func TestDefaultConfigUsesConfirmedActiveHighPolarity(t *testing.T) {
	if defaultConfig().ActiveLow {
		t.Fatal("confirmed relay polarity must default to active-high")
	}
}

func TestParseRelayStateMapsPhysicalLevelsForBothPolarities(t *testing.T) {
	tests := []struct {
		name      string
		activeLow bool
		level     string
		wantOn    bool
	}{
		{name: "active-high-high", activeLow: false, level: "high", wantOn: true},
		{name: "active-high-low", activeLow: false, level: "low", wantOn: false},
		{name: "active-low-high", activeLow: true, level: "high", wantOn: false},
		{name: "active-low-low", activeLow: true, level: "low", wantOn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRelayState(Config{ActiveLow: tt.activeLow}, "", tt.level)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantOn {
				t.Fatalf("parseRelayState(activeLow=%t, level=%q) = %t, want %t", tt.activeLow, tt.level, got, tt.wantOn)
			}
		})
	}
}

func (f *fakeSensor) ReadTemperature() (float64, error) {
	if f.readNotify != nil {
		close(f.readNotify)
		f.readNotify = nil
	}
	if len(f.readings) == 0 {
		return 0, errors.New("no reading")
	}
	reading := f.readings[f.index]
	if f.index < len(f.readings)-1 {
		f.index++
	}
	return reading.temp, reading.err
}

func TestRunChecksCancellationBeforeNextPoll(t *testing.T) {
	cfg := defaultConfig()
	cfg.PollInterval = time.Hour
	cfg.MinimumOn = 0
	cfg.MinimumOff = 0
	cfg.StatusInterval = 0
	output := &fakeOutput{}
	firstRead := make(chan struct{})
	sensor := &fakeSensor{
		readings:   []temperatureReading{{temp: 50}, {temp: 20}},
		readNotify: firstRead,
	}
	controller, err := NewController(cfg, output, sensor, log.New(io.Discard, "", 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	<-firstRead
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error after cancellation: %v", err)
	}
	if len(output.states) != 1 || !output.states[0] {
		t.Fatalf("queued cancellation must prevent the next OFF write: %v", output.states)
	}
	if err := controller.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func testController(t *testing.T, readings ...temperatureReading) (*Controller, *fakeOutput) {
	t.Helper()
	cfg := defaultConfig()
	cfg.MinimumOn = 120 * time.Second
	cfg.MinimumOff = 60 * time.Second
	output := &fakeOutput{}
	sensor := &fakeSensor{readings: readings}
	controller, err := NewController(cfg, output, sensor, log.New(io.Discard, "", 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	return controller, output
}

func TestThermostatStrictThresholdBoundariesPreserveState(t *testing.T) {
	c, output := testController(t,
		temperatureReading{temp: 40},
		temperatureReading{temp: 30},
		temperatureReading{temp: 29.9},
	)
	base := time.Unix(100, 0)
	if obs := c.Step(base.Add(121 * time.Second)); obs.Transition {
		t.Fatalf("40C must preserve ON state: %+v", obs)
	}
	if obs := c.Step(base.Add(122 * time.Second)); obs.Transition {
		t.Fatalf("30C must preserve ON state: %+v", obs)
	}
	if obs := c.Step(base.Add(123 * time.Second)); !obs.Transition || obs.ActualOn {
		t.Fatalf("temperature below 30C should turn cooling OFF: %+v", obs)
	}
	if len(output.states) != 2 || output.states[0] != true || output.states[1] != false {
		t.Fatalf("unexpected output states: %v", output.states)
	}
}

func TestMinimumOnDwellBlocksCoolingOff(t *testing.T) {
	c, output := testController(t, temperatureReading{temp: 20}, temperatureReading{temp: 20})
	base := time.Unix(100, 0)
	if obs := c.Step(base.Add(119 * time.Second)); obs.Transition {
		t.Fatalf("cooling changed before minimum ON dwell: %+v", obs)
	}
	if obs := c.Step(base.Add(120 * time.Second)); !obs.Transition || obs.ActualOn {
		t.Fatalf("cooling should turn OFF at minimum ON dwell: %+v", obs)
	}
	if len(output.states) != 2 {
		t.Fatalf("expected startup and one transition, got %v", output.states)
	}
}

func TestMinimumOffDwellBlocksCoolingOn(t *testing.T) {
	c, output := testController(t, temperatureReading{temp: 20}, temperatureReading{temp: 50}, temperatureReading{temp: 50})
	base := time.Unix(100, 0)
	if obs := c.Step(base.Add(120 * time.Second)); !obs.Transition || obs.ActualOn {
		t.Fatalf("setup should turn cooling OFF: %+v", obs)
	}
	if obs := c.Step(base.Add(179 * time.Second)); obs.Transition {
		t.Fatalf("cooling changed before minimum OFF dwell: %+v", obs)
	}
	if obs := c.Step(base.Add(180 * time.Second)); !obs.Transition || !obs.ActualOn {
		t.Fatalf("cooling should turn ON at minimum OFF dwell: %+v", obs)
	}
	if len(output.states) != 3 {
		t.Fatalf("expected startup and two transitions, got %v", output.states)
	}
}

func TestEmergencyTemperatureBypassesMinimumOffDwell(t *testing.T) {
	c, output := testController(t, temperatureReading{temp: 20}, temperatureReading{temp: 65})
	base := time.Unix(100, 0)
	if obs := c.Step(base.Add(120 * time.Second)); !obs.Transition || obs.ActualOn {
		t.Fatalf("setup should turn cooling OFF: %+v", obs)
	}
	if obs := c.Step(base.Add(121 * time.Second)); !obs.Transition || !obs.ActualOn {
		t.Fatalf("65C should bypass OFF dwell: %+v", obs)
	}
	if len(output.states) != 3 {
		t.Fatalf("expected startup and two transitions, got %v", output.states)
	}
}

func TestSensorFailureForcesCoolingOnRegardlessOfDwell(t *testing.T) {
	c, output := testController(t, temperatureReading{temp: 20}, temperatureReading{err: errors.New("sensor unavailable")})
	base := time.Unix(100, 0)
	if obs := c.Step(base.Add(120 * time.Second)); !obs.Transition || obs.ActualOn {
		t.Fatalf("setup should turn cooling OFF: %+v", obs)
	}
	obs := c.Step(base.Add(121 * time.Second))
	if !obs.Transition || !obs.ActualOn || obs.SensorError == nil {
		t.Fatalf("sensor failure must force cooling ON: %+v", obs)
	}
	if len(output.states) != 3 {
		t.Fatalf("expected startup and two transitions, got %v", output.states)
	}
}

func TestStartupAssertsCoolingOnBeforeFirstSensorRead(t *testing.T) {
	c, output := testController(t, temperatureReading{err: errors.New("no sensor")})
	if len(output.states) != 1 || !output.states[0] {
		t.Fatalf("startup must assert ON: %v", output.states)
	}
	obs := c.Step(time.Unix(101, 0))
	if !obs.RequestedOn || !obs.ActualOn || obs.SensorError == nil {
		t.Fatalf("startup sensor failure should retain safe ON: %+v", obs)
	}
}

func TestShutdownAssertsOnBeforeClosingOutput(t *testing.T) {
	c, output := testController(t, temperatureReading{temp: 20})
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if len(output.states) != 2 || !output.states[1] || !output.closed {
		t.Fatalf("shutdown must assert ON then close: states=%v closed=%t", output.states, output.closed)
	}
}

func TestInvalidConfigurationRejected(t *testing.T) {
	cfg := defaultConfig()
	cfg.OffThreshold = cfg.OnThreshold
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected equal thresholds to be rejected")
	}
	cfg = defaultConfig()
	cfg.EmergencyTemp = cfg.OnThreshold - 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected emergency threshold below ON threshold to be rejected")
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

type CoolingOutput interface {
	Set(on bool) error
	Close() error
}

type Observation struct {
	When        time.Time
	Temperature *float64
	SensorError error
	OutputError error
	RequestedOn bool
	ActualOn    bool
	Transition  bool
}

type Controller struct {
	cfg    Config
	output CoolingOutput
	sensor TemperatureSensor
	logger *log.Logger
	notify *SystemdNotifier
	now    func() time.Time

	mu         sync.Mutex
	started    bool
	closed     bool
	actualOn   bool
	lastChange time.Time
	lastStatus time.Time
}

func NewController(cfg Config, output CoolingOutput, sensor TemperatureSensor, logger *log.Logger, notify *SystemdNotifier) (*Controller, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if output == nil {
		return nil, fmt.Errorf("cooling output is nil")
	}
	if sensor == nil {
		return nil, fmt.Errorf("temperature sensor is nil")
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Controller{cfg: cfg, output: output, sensor: sensor, logger: logger, notify: notify, now: time.Now}, nil
}

func (c *Controller) Start(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return fmt.Errorf("controller already started")
	}
	if err := c.output.Set(true); err != nil {
		return fmt.Errorf("assert cooling output at startup: %w", err)
	}
	c.started = true
	c.actualOn = true
	c.lastChange = now
	c.logger.Printf("cooling transition actual=on requested=on reason=startup polarity=%s", c.polarity())
	return nil
}

func (c *Controller) Step(now time.Time) Observation {
	c.mu.Lock()
	defer c.mu.Unlock()
	obs := Observation{When: now, ActualOn: c.actualOn}
	if !c.started || c.closed {
		obs.SensorError = fmt.Errorf("controller is not running")
		obs.RequestedOn = true
		return obs
	}
	temp, sensorErr := c.sensor.ReadTemperature()
	if sensorErr == nil && (math.IsNaN(temp) || math.IsInf(temp, 0) || temp < thermalMinimumCelsius || temp > thermalMaximumCelsius) {
		sensorErr = fmt.Errorf("temperature outside sane range [%.0f, %.0f] C: %.3f", thermalMinimumCelsius, thermalMaximumCelsius, temp)
	}
	if sensorErr != nil {
		obs.SensorError = sensorErr
		obs.RequestedOn = true
	} else {
		obs.Temperature = &temp
		switch {
		case temp > c.cfg.OnThreshold:
			obs.RequestedOn = true
		case temp < c.cfg.OffThreshold:
			obs.RequestedOn = false
		default:
			// At either boundary, and throughout the hysteresis band, retain
			// the current state exactly.
			obs.RequestedOn = c.actualOn
		}
	}
	if obs.RequestedOn != c.actualOn && c.canTransition(now, obs.RequestedOn, temp, sensorErr != nil) {
		if err := c.output.Set(obs.RequestedOn); err != nil {
			obs.OutputError = fmt.Errorf("set cooling output to %s: %w", onOff(obs.RequestedOn), err)
			obs.RequestedOn = true
			return obs
		}
		c.actualOn = obs.RequestedOn
		c.lastChange = now
		obs.Transition = true
	}
	obs.ActualOn = c.actualOn
	return obs
}

func (c *Controller) canTransition(now time.Time, requestedOn bool, temp float64, sensorFailed bool) bool {
	if sensorFailed && requestedOn {
		return true
	}
	if now.Before(c.lastChange) {
		return false
	}
	elapsed := now.Sub(c.lastChange)
	if requestedOn {
		return elapsed >= c.cfg.MinimumOff || temp >= c.cfg.EmergencyTemp
	}
	return elapsed >= c.cfg.MinimumOn
}

func (c *Controller) Run(ctx context.Context) error {
	if err := c.Start(c.now()); err != nil {
		return err
	}
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Handle a queued shutdown before sampling or changing the relay. The
		// deferred Shutdown path will assert ON before releasing the line.
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		obs := c.Step(c.now())
		if obs.OutputError != nil {
			return obs.OutputError
		}
		c.logObservation(obs)
		if c.notify != nil {
			c.notify.MarkProgress()
			if err := c.notify.Ready(); err != nil {
				c.logger.Printf("systemd readiness notification failed: %v", err)
			}
			c.notify.StartWatchdog(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *Controller) logObservation(obs Observation) {
	if obs.Transition {
		c.logger.Printf("cooling transition actual=%s requested=%s temperature=%s polarity=%s", onOff(obs.ActualOn), onOff(obs.RequestedOn), formatTemperature(obs), c.polarity())
	}
	c.mu.Lock()
	shouldStatus := c.lastStatus.IsZero() || (c.cfg.StatusInterval > 0 && obs.When.Sub(c.lastStatus) >= c.cfg.StatusInterval)
	if shouldStatus {
		c.lastStatus = obs.When
	}
	c.mu.Unlock()
	if shouldStatus {
		status := fmt.Sprintf("cooling status temperature=%s requested=%s actual=%s polarity=%s", formatTemperature(obs), onOff(obs.RequestedOn), onOff(obs.ActualOn), c.polarity())
		if obs.SensorError != nil {
			status += fmt.Sprintf(" sensor_error=%q", obs.SensorError)
		}
		c.logger.Print(status)
		if c.notify != nil {
			if err := c.notify.Status(status); err != nil {
				c.logger.Printf("systemd status notification failed: %v", err)
			}
		}
	}
}

func (c *Controller) Shutdown() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	var firstErr error
	if err := c.output.Set(true); err != nil {
		firstErr = fmt.Errorf("assert cooling output before release: %w", err)
	}
	if err := c.output.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (c *Controller) polarity() string {
	if c.cfg.ActiveLow {
		return "active-low"
	}
	return "active-high"
}

func formatTemperature(obs Observation) string {
	if obs.Temperature == nil {
		return "invalid"
	}
	return fmt.Sprintf("%.1fC", *obs.Temperature)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

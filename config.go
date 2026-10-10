package main

import (
	"fmt"
	"math"
	"time"
)

const (
	defaultChipLabel      = "pinctrl-rp1"
	defaultGPIO           = 17
	defaultThermalRoot    = "/sys/class/thermal"
	defaultPollInterval   = 2 * time.Second
	defaultMinimumOn      = 120 * time.Second
	defaultMinimumOff     = 60 * time.Second
	defaultStatusInterval = 30 * time.Second
	defaultOnThreshold    = 40.0
	defaultOffThreshold   = 30.0
	defaultEmergencyTemp  = 65.0
	thermalZoneType       = "cpu-thermal"
	thermalMinimumCelsius = -50.0
	thermalMaximumCelsius = 150.0
)

// Config contains the daemon's hardware and thermostat settings.
type Config struct {
	ChipLabel      string
	GPIO           int
	ActiveLow      bool
	ThermalRoot    string
	PollInterval   time.Duration
	MinimumOn      time.Duration
	MinimumOff     time.Duration
	StatusInterval time.Duration
	OnThreshold    float64
	OffThreshold   float64
	EmergencyTemp  float64
}

func (c Config) Validate() error {
	if c.ChipLabel == "" {
		return fmt.Errorf("chip label must not be empty")
	}
	if c.GPIO < 0 {
		return fmt.Errorf("gpio must be non-negative: %d", c.GPIO)
	}
	if c.ThermalRoot == "" {
		return fmt.Errorf("thermal root must not be empty")
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive: %s", c.PollInterval)
	}
	if c.MinimumOn < 0 || c.MinimumOff < 0 {
		return fmt.Errorf("minimum dwell times must be non-negative: on=%s off=%s", c.MinimumOn, c.MinimumOff)
	}
	if c.StatusInterval < 0 {
		return fmt.Errorf("status interval must be non-negative: %s", c.StatusInterval)
	}
	for name, value := range map[string]float64{
		"on threshold":   c.OnThreshold,
		"off threshold":  c.OffThreshold,
		"emergency temp": c.EmergencyTemp,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s must be finite", name)
		}
	}
	if c.OffThreshold >= c.OnThreshold {
		return fmt.Errorf("off threshold must be below on threshold: off=%.3g on=%.3g", c.OffThreshold, c.OnThreshold)
	}
	if c.EmergencyTemp < c.OnThreshold {
		return fmt.Errorf("emergency temperature must be at least on threshold: emergency=%.3g on=%.3g", c.EmergencyTemp, c.OnThreshold)
	}
	return nil
}

func defaultConfig() Config {
	return Config{
		ChipLabel:      defaultChipLabel,
		GPIO:           defaultGPIO,
		ActiveLow:      false,
		ThermalRoot:    defaultThermalRoot,
		PollInterval:   defaultPollInterval,
		MinimumOn:      defaultMinimumOn,
		MinimumOff:     defaultMinimumOff,
		StatusInterval: defaultStatusInterval,
		OnThreshold:    defaultOnThreshold,
		OffThreshold:   defaultOffThreshold,
		EmergencyTemp:  defaultEmergencyTemp,
	}
}

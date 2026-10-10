package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// TemperatureSensor reads a CPU temperature in degrees Celsius.
type TemperatureSensor interface {
	ReadTemperature() (float64, error)
}

// FileTemperatureSensor locates the thermal zone by its type on every read.
// Re-scanning allows the daemon to recover if the thermal zone appears after
// boot or a sysfs entry is temporarily unavailable.
type FileTemperatureSensor struct {
	Root string
}

func (s FileTemperatureSensor) ReadTemperature() (float64, error) {
	tempPath, err := findThermalZoneTemp(s.Root, thermalZoneType)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", tempPath, err)
	}
	temp, err := parseTemperature(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", tempPath, err)
	}
	return temp, nil
}

func findThermalZoneTemp(root, wantedType string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("scan thermal zones in %s: %w", root, err)
	}
	// ReadDir is sorted by filename, but sorting explicitly keeps this helper
	// deterministic for alternate directory implementations used in tests.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "thermal_zone") {
			continue
		}
		zonePath := filepath.Join(root, entry.Name())
		zoneInfo, err := os.Stat(zonePath) // follow the symlink used by sysfs
		if err != nil || !zoneInfo.IsDir() {
			continue
		}
		typeData, err := os.ReadFile(filepath.Join(zonePath, "type"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(typeData)) == wantedType {
			return filepath.Join(zonePath, "temp"), nil
		}
	}
	return "", fmt.Errorf("thermal zone type %q not found in %s", wantedType, root)
}

func parseTemperature(value string) (float64, error) {
	if value == "" {
		return 0, fmt.Errorf("empty temperature")
	}
	// Linux thermal sysfs reports millidegrees Celsius. Keep the conversion
	// explicit so a malformed small integer cannot be mistaken for Celsius.
	raw, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid temperature %q: %w", value, err)
	}
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 0, fmt.Errorf("temperature is not finite: %q", value)
	}
	temp := raw / 1000
	if math.IsNaN(temp) || math.IsInf(temp, 0) || temp < thermalMinimumCelsius || temp > thermalMaximumCelsius {
		return 0, fmt.Errorf("temperature outside sane range [%.0f, %.0f] C: %.3f", thermalMinimumCelsius, thermalMaximumCelsius, temp)
	}
	return temp, nil
}

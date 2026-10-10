package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThermalSensorFindsSymlinkedCPUZoneAndConvertsMillidegrees(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual-cpu-zone")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "type"), []byte("cpu-thermal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "temp"), []byte("42500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("actual-cpu-zone", filepath.Join(root, "thermal_zone0")); err != nil {
		t.Fatal(err)
	}
	temp, err := (FileTemperatureSensor{Root: root}).ReadTemperature()
	if err != nil {
		t.Fatal(err)
	}
	if temp != 42.5 {
		t.Fatalf("expected 42.5C, got %.3f", temp)
	}
}

func TestThermalSensorRejectsInvalidAndInsaneValues(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "200000", "bad"} {
		if _, err := parseTemperature(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

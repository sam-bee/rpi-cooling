package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Version is replaced by packaging with -ldflags when a release is built.
var Version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg := defaultConfig()
	var (
		showVersion bool
		checkOnly   bool
		showStatus  bool
		relaySet    string
		relayLevel  string
		force       string
		probe       bool
		hold        time.Duration
	)
	fs := flag.NewFlagSet("rpi-cooling", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: rpi-cooling [options]")
		fmt.Fprintln(fs.Output(), "Run the Raspberry Pi CPU-temperature cooling controller.")
		fs.PrintDefaults()
	}
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&checkOnly, "check-only", false, "discover GPIO and CPU thermal hardware without requesting or changing GPIO")
	fs.BoolVar(&showStatus, "status", false, "print read-only GPIO discovery and current CPU temperature, then exit")
	fs.StringVar(&relaySet, "relay-set", "", "explicit logical relay state: on or off (holds the requested line while --hold runs)")
	fs.StringVar(&relayLevel, "relay-level", "", "explicit physical relay level: high or low (holds the requested line while --hold runs)")
	fs.StringVar(&force, "force", "", "alias for --relay-set, useful for explicit tests: on or off")
	fs.BoolVar(&probe, "probe", false, "explicit probe mode; requires --relay-level")
	fs.DurationVar(&hold, "hold", 0, "duration to hold an explicit relay state before releasing it (default 0)")
	fs.StringVar(&cfg.ChipLabel, "chip-label", cfg.ChipLabel, "GPIO chip label to discover")
	fs.IntVar(&cfg.GPIO, "gpio", cfg.GPIO, "BCM GPIO offset")
	fs.BoolVar(&cfg.ActiveLow, "active-low", cfg.ActiveLow, "treat a physical low GPIO level as cooling ON")
	fs.StringVar(&cfg.ThermalRoot, "thermal-root", cfg.ThermalRoot, "thermal sysfs root")
	fs.DurationVar(&cfg.PollInterval, "poll", cfg.PollInterval, "temperature polling interval")
	fs.DurationVar(&cfg.MinimumOn, "min-on", cfg.MinimumOn, "minimum cooling ON dwell")
	fs.DurationVar(&cfg.MinimumOff, "min-off", cfg.MinimumOff, "minimum cooling OFF dwell")
	fs.DurationVar(&cfg.StatusInterval, "status-interval", cfg.StatusInterval, "periodic status log interval; 0 disables periodic status after the first")
	fs.Float64Var(&cfg.OnThreshold, "on-threshold", cfg.OnThreshold, "turn cooling ON strictly above this Celsius temperature")
	fs.Float64Var(&cfg.OffThreshold, "off-threshold", cfg.OffThreshold, "turn cooling OFF strictly below this Celsius temperature")
	fs.Float64Var(&cfg.EmergencyTemp, "emergency-threshold", cfg.EmergencyTemp, "turn cooling ON immediately at or above this Celsius temperature")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "configuration error: unexpected positional arguments: %v\n", fs.Args())
		return 2
	}
	if showVersion {
		fmt.Println(Version)
		return 0
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	if hold < 0 {
		fmt.Fprintf(os.Stderr, "configuration error: hold must be non-negative: %s\n", hold)
		return 2
	}
	if force != "" {
		if relaySet != "" {
			fmt.Fprintln(os.Stderr, "configuration error: --force and --relay-set cannot be used together")
			return 2
		}
		relaySet = force
	}
	if checkOnly || showStatus {
		if relaySet != "" || relayLevel != "" || probe || hold != 0 {
			fmt.Fprintln(os.Stderr, "configuration error: read-only mode cannot be combined with relay controls")
			return 2
		}
		return runReadOnly(cfg, showStatus)
	}
	if probe && relayLevel == "" {
		fmt.Fprintln(os.Stderr, "configuration error: --probe requires --relay-level=high or --relay-level=low")
		return 2
	}
	if probe && relaySet != "" {
		fmt.Fprintln(os.Stderr, "configuration error: --probe and --relay-set cannot be used together")
		return 2
	}
	if relaySet != "" || relayLevel != "" {
		return runRelayMode(cfg, relaySet, relayLevel, hold)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	return runDaemon(cfg)
}

func runReadOnly(cfg Config, status bool) int {
	chipName, info, err := CheckGPIO(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GPIO discovery failed: %v\n", err)
		return 1
	}
	temp, tempErr := (FileTemperatureSensor{Root: cfg.ThermalRoot}).ReadTemperature()
	if status {
		fmt.Printf("gpio_chip=%s chip_label=%s gpio=%d line_name=%q used=%t active_low=%t\n", chipName, cfg.ChipLabel, cfg.GPIO, info.Name, info.Used, cfg.ActiveLow)
		if tempErr != nil {
			fmt.Printf("temperature=invalid error=%q\n", tempErr)
			return 1
		} else {
			fmt.Printf("temperature=%.1fC\n", temp)
		}
	} else if tempErr != nil {
		fmt.Fprintf(os.Stderr, "thermal discovery failed: %v\n", tempErr)
		return 1
	}
	return 0
}

func runDaemon(cfg Config) int {
	logger := log.New(os.Stderr, "rpi-cooling: ", log.LstdFlags)
	notify, err := NewSystemdNotifier(logger)
	if err != nil {
		logger.Printf("configuration error: %v", err)
		return 2
	}
	output, err := OpenGPIO(cfg, true)
	if err != nil {
		logger.Printf("GPIO startup failed: %v", err)
		return 1
	}
	controller, err := NewController(cfg, output, FileTemperatureSensor{Root: cfg.ThermalRoot}, logger, notify)
	if err != nil {
		_ = output.Set(true)
		_ = output.Close()
		logger.Printf("controller configuration failed: %v", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	runErr := controller.Run(ctx)
	shutdownErr := controller.Shutdown()
	if runErr != nil {
		logger.Printf("controller stopped: %v", runErr)
	}
	if shutdownErr != nil {
		logger.Printf("shutdown failed: %v", shutdownErr)
	}
	if runErr != nil || shutdownErr != nil {
		return 1
	}
	return 0
}

func runRelayMode(cfg Config, relaySet, relayLevel string, hold time.Duration) int {
	logicalOn, err := parseRelayState(cfg, relaySet, relayLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	output, err := OpenGPIO(cfg, logicalOn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GPIO probe failed: %v\n", err)
		return 1
	}
	logger := log.New(os.Stderr, "rpi-cooling: ", log.LstdFlags)
	logger.Printf("explicit relay test logical=%s physical=%s hold=%s polarity=%s", onOff(logicalOn), physicalLevel(logicalOn, cfg.ActiveLow), hold, polarity(cfg.ActiveLow))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if hold > 0 {
		timer := time.NewTimer(hold)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	var firstErr error
	if err := output.Set(true); err != nil {
		firstErr = fmt.Errorf("assert cooling output before release: %w", err)
	}
	if err := output.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		fmt.Fprintf(os.Stderr, "relay test shutdown failed: %v\n", firstErr)
		return 1
	}
	return 0
}

func parseRelayState(cfg Config, relaySet, relayLevel string) (bool, error) {
	if relaySet != "" && relayLevel != "" {
		return false, fmt.Errorf("--relay-set and --relay-level cannot be used together")
	}
	if relaySet != "" {
		switch strings.ToLower(relaySet) {
		case "on":
			return true, nil
		case "off":
			return false, nil
		default:
			return false, fmt.Errorf("invalid --relay-set=%q: use on or off", relaySet)
		}
	}
	if relayLevel != "" {
		switch strings.ToLower(relayLevel) {
		case "high":
			return !cfg.ActiveLow, nil
		case "low":
			return cfg.ActiveLow, nil
		default:
			return false, fmt.Errorf("invalid --relay-level=%q: use high or low", relayLevel)
		}
	}
	return false, fmt.Errorf("relay mode requires --relay-set or --relay-level")
}

func polarity(activeLow bool) string {
	if activeLow {
		return "active-low"
	}
	return "active-high"
}

func physicalLevel(logicalOn, activeLow bool) string {
	physicalHigh := logicalOn != activeLow
	if physicalHigh {
		return "high"
	}
	return "low"
}

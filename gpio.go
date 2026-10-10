package main

import (
	"fmt"

	"github.com/warthog618/go-gpiocdev"
)

// GPIOOutput owns an exclusively requested GPIO line until Close.
type GPIOOutput struct {
	chip *gpiocdev.Chip
	line *gpiocdev.Line
}

func discoverGPIO(cfg Config) (*gpiocdev.Chip, *gpiocdev.LineInfo, error) {
	chips := gpiocdev.Chips()
	var match string
	for _, name := range chips {
		chip, err := gpiocdev.NewChip(name)
		if err != nil {
			return nil, nil, fmt.Errorf("open %s while looking for label %q: %w", name, cfg.ChipLabel, err)
		}
		label := chip.Label
		if label == cfg.ChipLabel {
			if match != "" {
				chip.Close()
				return nil, nil, fmt.Errorf("multiple gpio chips have label %q: %s and %s", cfg.ChipLabel, match, name)
			}
			match = name
		}
		if err := chip.Close(); err != nil {
			return nil, nil, fmt.Errorf("close %s after discovery: %w", name, err)
		}
	}
	if match == "" {
		return nil, nil, fmt.Errorf("gpio chip with label %q not found (available: %v)", cfg.ChipLabel, chips)
	}
	chip, err := gpiocdev.NewChip(match)
	if err != nil {
		return nil, nil, fmt.Errorf("open gpio chip %s: %w", match, err)
	}
	info, err := chip.LineInfo(cfg.GPIO)
	if err != nil {
		chip.Close()
		return nil, nil, fmt.Errorf("inspect gpio %s line %d: %w", match, cfg.GPIO, err)
	}
	return chip, &info, nil
}

// CheckGPIO discovers the labelled chip and line without requesting or
// changing the line. It is safe for --check-only and --status.
func CheckGPIO(cfg Config) (chipName string, info gpiocdev.LineInfo, err error) {
	chip, lineInfo, err := discoverGPIO(cfg)
	if err != nil {
		return "", gpiocdev.LineInfo{}, err
	}
	defer chip.Close()
	return chip.Name, *lineInfo, nil
}

func OpenGPIO(cfg Config, initialOn bool) (*GPIOOutput, error) {
	chip, _, err := discoverGPIO(cfg)
	if err != nil {
		return nil, err
	}
	options := []gpiocdev.LineReqOption{
		gpiocdev.WithConsumer("rpi-cooling"),
		gpiocdev.AsOutput(boolToLogical(initialOn)),
	}
	if cfg.ActiveLow {
		options = append(options, gpiocdev.AsActiveLow)
	}
	line, err := chip.RequestLine(cfg.GPIO, options...)
	if err != nil {
		chip.Close()
		return nil, fmt.Errorf("request gpio chip %s line %d: %w", chip.Name, cfg.GPIO, err)
	}
	return &GPIOOutput{chip: chip, line: line}, nil
}

func (o *GPIOOutput) Set(on bool) error {
	if o == nil || o.line == nil {
		return fmt.Errorf("gpio output is not open")
	}
	if err := o.line.SetValue(boolToLogical(on)); err != nil {
		return fmt.Errorf("set cooling gpio: %w", err)
	}
	return nil
}

func (o *GPIOOutput) Close() error {
	if o == nil {
		return nil
	}
	var firstErr error
	if o.line != nil {
		if err := o.line.Close(); err != nil {
			firstErr = fmt.Errorf("close gpio line: %w", err)
		}
		o.line = nil
	}
	if o.chip != nil {
		if err := o.chip.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close gpio chip: %w", err)
		}
		o.chip = nil
	}
	return firstErr
}

func boolToLogical(on bool) int {
	if on {
		return 1
	}
	return 0
}

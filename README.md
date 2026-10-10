# rpi-cooling

`rpi-cooling` is a small Go controller for the Raspberry Pi 5 cooling relay. It reads the CPU temperature and owns the GPIO line continuously while the service runs. The intended repository is `git@github.com:sam-bee/rpi-cooling.git`; on the Pi the source checkout is `/home/sierra/workfiles/tech-projects/rpi-cooling`.

The hardware currently uses physical header pin 11 (BCM GPIO17), with relay VCC on physical pin 2 (5 V) and relay ground on physical pin 6 (GND). The relay contacts are wired normally open. The manual always-on bypass has been removed; a controlled test confirmed that this module energises when its GPIO input is LOW, so the deployed configuration uses `--active-low=true`.

## Control contract

The controller uses strict thresholds:

- request cooling ON only when the temperature is greater than 40 °C;
- request cooling OFF only when the temperature is less than 30 °C;
- retain the current state between those thresholds;
- sample every 2 seconds;
- keep cooling on for at least 120 seconds after activation;
- keep cooling off for at least 60 seconds after deactivation;
- at or above 65 °C, bypass the normal off dwell;
- request ON at startup and whenever the temperature sensor fails.

The GPIO chip is selected by the `pinctrl-rp1` label for the Pi 5 RP1 GPIO controller. The line is GPIO17 even though the physical header position is pin 11. `--active-low` is configurable; it cannot be inferred from a normally open contact description. The installed module was tested directly: LOW energises cooling and HIGH de-energises it, so `--active-low=true` is the deliberate setting for this wiring.

The requested GPIO state cannot confirm that the cooling circuit has flowed or that the relay contacts have closed. Normally-open contacts leave cooling off when the relay is de-energised; software cannot guarantee cooling ON during a process crash, power loss, or reset. Retain a way to restore cooling manually while commissioning the pump and service.

## Build

Omarchy provides Go through mise. Select the requested toolchain in the user environment before building:

```sh
mise use --global go@1.27.2
go version
go test ./...
go vet ./...
mkdir -p build
CGO_ENABLED=0 go build -trimpath -ldflags "-X main.Version=$(git rev-parse --short HEAD)" -o build/rpi-cooling .
./build/rpi-cooling --version
```

The build embeds the short source commit in `--version` (or `dev` when the checkout has no commit yet). The resulting `build/rpi-cooling` binary is self-contained for runtime purposes. The Pi does not need mise, Go, or a compiler after installation.

The daemon reads command-line flags. The shipped example passes the deployment values explicitly: `--chip-label`, `--gpio`, `--active-low`, `--thermal-root`, `--poll`, `--min-on`, `--min-off`, `--status-interval`, `--on-threshold`, `--off-threshold`, and `--emergency-threshold`. For read-only diagnostics use `--check-only` or `--status`; relay tests use `--relay-set=on|off`, `--force=on|off`, or `--probe --relay-level=high|low` with optional `--hold=duration`. Explicit relay modes return the requested line to logical ON before releasing it.

## Install on the Pi

Build from `/home/sierra/workfiles/tech-projects/rpi-cooling`, then install as root:

```sh
sudo install -o root -g root -m 0755 build/rpi-cooling /usr/local/bin/rpi-cooling
sudo install -o root -g root -m 0644 systemd/rpi-cooling.service /etc/systemd/system/rpi-cooling.service
if ! sudo test -e /etc/rpi-cooling.conf; then
  sudo install -o root -g root -m 0600 exampleconfig /etc/rpi-cooling.conf
fi
sudo systemctl daemon-reload
sudo systemctl enable --now rpi-cooling.service
```

These commands write the root-owned binary to `/usr/local/bin/rpi-cooling`, the unit to `/etc/systemd/system/rpi-cooling.service`, and create `/etc/rpi-cooling.conf` from [`exampleconfig`](exampleconfig) only when no configuration exists. The configuration is a systemd `EnvironmentFile` containing one `RPI_COOLING_FLAGS` line; the unit expands those flags when starting the binary. They set the binary and unit to mode 0755/0644 and a newly created configuration to mode 0600. An existing configuration is preserved so an update cannot silently change `--active-low` or another deliberate setting.

If GNU make is available, `make build`, `sudo make install`, and `sudo make enable` are equivalent convenience targets. The Pi build above needs only Go and Git. `make install` saves the previous binary and unit under `/var/backups/rpi-cooling/`. To restore those two files after reviewing the change:

```sh
sudo systemctl disable --now rpi-cooling.service
sudo make rollback
sudo systemctl enable --now rpi-cooling.service
```

Rollback does not overwrite `/etc/rpi-cooling.conf`. If the configuration must change, edit the `RPI_COOLING_FLAGS` line deliberately, inspect `--active-low=true`, then reload the service:

```sh
sudoedit /etc/rpi-cooling.conf
sudo systemctl restart rpi-cooling.service
```

The service runs as root so it can request the GPIO line, holds that line for its lifetime, and uses `Type=notify` with a 15-second systemd watchdog. It is configured to restart after a failure. The unit intentionally does not use `PrivateDevices` or a sandbox that hides GPIO character devices. On a graceful SIGTERM, the controller makes a best-effort request for cooling ON while it still owns the line, then releases the line; the relay state after release is unknown. The unit has no `ExecStop` or `ExecStopPost` action and never forces an unconditional relay OFF on stop.

## Inspect and operate the service

```sh
systemctl status rpi-cooling.service
journalctl -u rpi-cooling.service -f
sudo systemctl restart rpi-cooling.service
sudo systemctl disable --now rpi-cooling.service
```

The service invocation reads `/etc/rpi-cooling.conf` as a systemd environment file and supplies the resulting flags to the binary. The relay polarity test confirmed `--active-low=true`; recheck this setting before moving the controller to different relay hardware.

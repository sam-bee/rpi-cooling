APP ?= rpi-cooling
GO ?= go
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf '%s' dev)
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
SYSCONFDIR ?= /etc
SYSTEMD_UNIT_DIR ?= /etc/systemd/system
BUILD_DIR ?= build
CONFIG_FILE ?= $(SYSCONFDIR)/rpi-cooling.conf
UNIT_FILE ?= $(SYSTEMD_UNIT_DIR)/rpi-cooling.service
BACKUP_DIR ?= /var/backups/rpi-cooling

.PHONY: all build test clean install rollback daemon-reload enable disable

all: build

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.Version=$(VERSION)" -o $(BUILD_DIR)/$(APP) ./...

test:
	$(GO) test ./...

clean:
	rm -rf $(BUILD_DIR)

# Install files only. The existing configuration is deliberately preserved;
# review and update it explicitly when changing --active-low or other settings.
install:
	@test "$$(id -u)" -eq 0 || { echo "make install must run as root (try sudo make install)" >&2; exit 1; }
	test -x "$(BUILD_DIR)/$(APP)" || { echo "$(BUILD_DIR)/$(APP) is missing; build it as the Go toolchain user before sudo make install" >&2; exit 1; }
	install -d -m 0755 "$(BINDIR)" "$(SYSTEMD_UNIT_DIR)" "$(BACKUP_DIR)"
	if test -e "$(BINDIR)/$(APP)"; then cp -a "$(BINDIR)/$(APP)" "$(BACKUP_DIR)/$(APP).previous"; fi
	if test -e "$(UNIT_FILE)"; then cp -a "$(UNIT_FILE)" "$(BACKUP_DIR)/rpi-cooling.service.previous"; fi
	install -m 0755 "$(BUILD_DIR)/$(APP)" "$(BINDIR)/$(APP)"
	install -m 0644 systemd/rpi-cooling.service "$(UNIT_FILE)"
	if test -e "$(CONFIG_FILE)"; then \
		echo "preserving existing $(CONFIG_FILE); update it deliberately if required"; \
	else \
		install -m 0600 exampleconfig "$(CONFIG_FILE)"; \
	fi
	$(MAKE) daemon-reload

daemon-reload:
	@test "$$(id -u)" -eq 0 || { echo "daemon-reload must run as root" >&2; exit 1; }
	systemctl daemon-reload

enable:
	@test "$$(id -u)" -eq 0 || { echo "enable must run as root" >&2; exit 1; }
	systemctl enable --now rpi-cooling.service

disable:
	@test "$$(id -u)" -eq 0 || { echo "disable must run as root" >&2; exit 1; }
	systemctl disable --now rpi-cooling.service

# Restore the files saved by the most recent install. This does not touch the
# configuration: restore it manually only after reviewing the saved copy.
rollback:
	@test "$$(id -u)" -eq 0 || { echo "rollback must run as root" >&2; exit 1; }
	test -e "$(BACKUP_DIR)/$(APP).previous"
	test -e "$(BACKUP_DIR)/rpi-cooling.service.previous"
	install -m 0755 "$(BACKUP_DIR)/$(APP).previous" "$(BINDIR)/$(APP)"
	install -m 0644 "$(BACKUP_DIR)/rpi-cooling.service.previous" "$(UNIT_FILE)"
	$(MAKE) daemon-reload

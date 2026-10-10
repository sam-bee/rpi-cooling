package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// SystemdNotifier implements the small subset of sd_notify needed by the
// service. It uses Unix datagrams directly and has no systemd dependency.
type SystemdNotifier struct {
	socket       string
	watchdog     time.Duration
	lastProgress atomic.Int64
	started      atomic.Bool
	ready        atomic.Bool
	logger       *log.Logger
}

func NewSystemdNotifier(logger *log.Logger) (*SystemdNotifier, error) {
	socket := os.Getenv("NOTIFY_SOCKET")
	watchdogUsec := os.Getenv("WATCHDOG_USEC")
	var watchdog time.Duration
	if watchdogUsec != "" {
		usec, err := strconv.ParseInt(watchdogUsec, 10, 64)
		if err != nil || usec <= 0 || usec > int64((time.Duration(1<<63-1))/time.Microsecond) {
			return nil, fmt.Errorf("invalid WATCHDOG_USEC=%q", watchdogUsec)
		}
		watchdog = time.Duration(usec) * time.Microsecond
	}
	return &SystemdNotifier{socket: socket, watchdog: watchdog, logger: logger}, nil
}

func (n *SystemdNotifier) send(message string) error {
	if n == nil || n.socket == "" {
		return nil
	}
	name := n.socket
	if name[0] == '@' {
		name = "\x00" + name[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	_, err = conn.Write([]byte(message))
	return err
}

func (n *SystemdNotifier) Ready() error {
	if n == nil || n.socket == "" || n.ready.Load() {
		return nil
	}
	if err := n.send("READY=1\n"); err != nil {
		return err
	}
	n.ready.Store(true)
	return nil
}

func (n *SystemdNotifier) Status(status string) error {
	return n.send("STATUS=" + status + "\n")
}

// MarkProgress records successful controller progress. Watchdog messages are
// gated on this timestamp so a wedged controller never reports readiness or a
// heartbeat before its first completed poll.
func (n *SystemdNotifier) MarkProgress() {
	if n != nil {
		n.lastProgress.Store(time.Now().UnixNano())
	}
}

func (n *SystemdNotifier) StartWatchdog(ctx context.Context) {
	if n == nil || n.socket == "" || n.watchdog <= 0 || n.started.Swap(true) {
		return
	}
	interval := n.watchdog / 2
	if interval <= 0 {
		interval = time.Microsecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				last := n.lastProgress.Load()
				if last == 0 || time.Since(time.Unix(0, last)) >= n.watchdog {
					continue
				}
				if err := n.send("WATCHDOG=1\n"); err != nil && n.logger != nil {
					n.logger.Printf("systemd watchdog notification failed: %v", err)
				}
			}
		}
	}()
}

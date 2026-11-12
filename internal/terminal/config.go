// Package terminal provides a single-session PTY backed by a shell, bridged
// to a WebSocket connection. It is consumed by the frontend HTTP server to
// expose an interactive root shell to operators authenticated against the
// Web UI session store.
package terminal

import "time"

// Config controls how the manager spawns and supervises the shell session.
//
// Zero-value Config is invalid; use DefaultConfig() and override fields.
type Config struct {
	// Shell is the absolute path of the binary spawned at session start.
	// Default is /bin/login, matching OpenWrt's LuCI web terminal: login
	// prints /etc/banner, prompts for credentials, then execs the user's
	// login shell. Operators who want to skip the login prompt can
	// override to /bin/ash, /bin/sh, or /bin/bash.
	Shell string

	// Env is the environment passed to the shell process. The manager
	// always overrides TERM=xterm-256color and never inherits the parent
	// environment wholesale, to keep the shell's environment predictable.
	Env []string

	// GraceShutdown is the time the manager waits between sending SIGTERM
	// and SIGKILL to the shell process group when the session is being
	// torn down. Two seconds is enough for ash/bash to flush; longer
	// risks blocking the operator's tab close.
	GraceShutdown time.Duration

	// PongWait is how long the session waits for any frame, pong
	// included, before it treats the client as gone and ends. It frees
	// the single session slot when a tab disappears without closing.
	PongWait time.Duration

	// PingPeriod is how often the session pings the client. It must be
	// shorter than PongWait so a live client's pong arrives in time.
	PingPeriod time.Duration

	// MaxMessageSize is the largest WebSocket message, in bytes, the
	// session accepts from the client. A larger message ends the session.
	MaxMessageSize int64
}

// DefaultConfig returns a Config wired for OpenWrt + BusyBox.
func DefaultConfig() Config {
	return Config{
		Shell: "/bin/login",
		Env: []string{
			"TERM=xterm-256color",
			"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
			"HOME=/root",
			"LANG=C.UTF-8",
		},
		GraceShutdown:  2 * time.Second,
		PongWait:       defaultPongWait,
		PingPeriod:     defaultPongWait * 9 / 10,
		MaxMessageSize: 64 * 1024,
	}
}

// defaultPongWait matches the audio WebSocket's keepalive
// (internal/websocket/client.go).
const defaultPongWait = 60 * time.Second

// withKeepaliveDefaults returns c with any non-positive keepalive field
// replaced by its DefaultConfig value. A zero PongWait would time out every
// read at once and a zero PingPeriod would panic the ping ticker.
func (c Config) withKeepaliveDefaults() Config {
	def := DefaultConfig()

	if c.PongWait <= 0 {
		c.PongWait = def.PongWait
	}

	if c.PingPeriod <= 0 {
		c.PingPeriod = def.PingPeriod
	}

	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = def.MaxMessageSize
	}

	return c
}

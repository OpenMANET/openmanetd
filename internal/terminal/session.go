package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"
)

// pingWriteWait bounds how long writing one ping frame may block.
const pingWriteWait = 10 * time.Second

// resizeMsg is the JSON shape of the only currently supported control frame.
// Forward-compatible: unknown "type" values are logged and ignored.
type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Session owns one PTY, one shell process, and the bidirectional pump
// between the PTY and a single WebSocket connection. A Session is
// single-use; once Run returns, the Session must not be reused.
type Session struct {
	log zerolog.Logger
	ws  *websocket.Conn
	pty *os.File
	cmd *exec.Cmd
	cfg Config
}

// NewSession spawns the shell, allocates a PTY, and returns a Session ready
// for Run. Errors during spawn (missing shell, no /dev/ptmx) are returned
// before any goroutines are started.
func NewSession(ctx context.Context, log zerolog.Logger, cfg Config, ws *websocket.Conn) (*Session, error) {
	cmd := exec.CommandContext(ctx, cfg.Shell)
	cmd.Cancel = nil // session teardown sends signals to the process group;
	// suppress exec.Cmd's per-process Kill so teardown is centralized.
	cmd.Env = append([]string{}, cfg.Env...)

	// pty.Start unconditionally sets Setsid=true and Setctty=true on
	// cmd.SysProcAttr before spawning, so the shell runs in a new session
	// with the PTY as its controlling terminal. No explicit SysProcAttr is
	// needed here.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}

	return &Session{log: log, cfg: cfg.withKeepaliveDefaults(), ws: ws, pty: ptmx, cmd: cmd}, nil
}

// Run drives the PTY, WebSocket, and ping pumps and waits for the shell to
// exit. It returns when (a) ctx is canceled, (b) one of the pumps errors,
// including a client that stops answering pings within PongWait or sends a
// message over MaxMessageSize, or (c) the shell exits on its own. In all cases it tears down: cancels the derived
// context, closes the PTY, signals the shell's process group (SIGTERM
// → SIGKILL after GraceShutdown), and waits for cmd.Wait() to complete.
//
// Returns nil for clean shutdown (shell exited / ctx canceled); other
// errors describe the cause (PTY read error, WS write error).
func (s *Session) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Single Wait() for the shell, drained at the end.
	shellDone := make(chan error, 1)

	go func() { shellDone <- s.cmd.Wait() }()

	// One slot per pump so none blocks sending its result after Run has
	// stopped selecting on pumpErr.
	const pumps = 3

	pumpErr := make(chan error, pumps)

	go func() { pumpErr <- s.pumpPTYToWS(ctx) }()
	go func() { pumpErr <- s.pumpWSToPTY(ctx) }()
	go func() { pumpErr <- s.pumpPings(ctx) }()

	var firstErr error
	select {
	case <-ctx.Done():
		firstErr = ctx.Err()
	case firstErr = <-pumpErr:
	case <-shellDone:
		// Shell exited on its own (Ctrl-D, exit). The shellDone slot is
		// already consumed; set to nil so the teardown skips the Wait.
		shellDone = nil
	}

	cancel()

	_ = s.pty.Close() // Unblocks pumpPTYToWS blocked on PTY read.
	_ = s.ws.Close()  // Unblocks pumpWSToPTY blocked on NextReader.

	// Drain the pump goroutines so Run's contract is "all spawned goroutines
	// have exited" by the time it returns.
drainLoop:
	for range pumps {
		select {
		case <-pumpErr:
		case <-time.After(s.cfg.GraceShutdown):
			// Pump didn't exit despite WS/PTY being closed; defensive — bail
			// rather than hang forever.
			break drainLoop
		}
	}

	if shellDone != nil && s.cmd.Process != nil {
		pgid, perr := unix.Getpgid(s.cmd.Process.Pid)
		if perr != nil {
			pgid = s.cmd.Process.Pid
		}

		_ = unix.Kill(-pgid, unix.SIGTERM)

		select {
		case <-shellDone:
		case <-time.After(s.cfg.GraceShutdown):
			_ = unix.Kill(-pgid, unix.SIGKILL)

			<-shellDone
		}
	}

	return firstErr
}

// pumpPTYToWS reads from the PTY and writes BinaryMessage frames to the WS.
// Reuses a 1 KiB buffer to avoid per-iteration allocations.
func (s *Session) pumpPTYToWS(ctx context.Context) error {
	buf := make([]byte, 1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			if werr := s.ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				return fmt.Errorf("ws write: %w", werr)
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("pty read: %w", err)
		}

		if ctx.Err() != nil {
			return fmt.Errorf("pty pump ctx: %w", ctx.Err())
		}
	}
}

// pumpWSToPTY reads frames from the WS. BinaryMessage bytes are written
// directly to the PTY (stdin). TextMessage frames are decoded as control
// envelopes; only "resize" is recognized today. Reads fail, ending the
// session, when no pong arrives within PongWait or a message exceeds
// MaxMessageSize.
func (s *Session) pumpWSToPTY(ctx context.Context) error {
	s.ws.SetReadLimit(s.cfg.MaxMessageSize)

	if err := s.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait)); err != nil {
		return fmt.Errorf("ws read deadline: %w", err)
	}

	s.ws.SetPongHandler(func(string) error {
		return s.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	})

	for {
		if ctx.Err() != nil {
			return fmt.Errorf("ws pump ctx: %w", ctx.Err())
		}

		mt, r, err := s.ws.NextReader()
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}

		switch mt {
		case websocket.BinaryMessage:
			if _, err := io.Copy(s.pty, r); err != nil {
				return fmt.Errorf("pty write: %w", err)
			}
		case websocket.TextMessage:
			var msg resizeMsg
			if err := json.NewDecoder(r).Decode(&msg); err != nil {
				s.log.Debug().Err(err).Msg("terminal: malformed control frame; ignoring")

				continue
			}

			switch msg.Type {
			case "resize":
				if err := pty.Setsize(s.pty, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows}); err != nil {
					s.log.Debug().Err(err).Msg("terminal: pty.Setsize failed")
				}
			default:
				s.log.Debug().Str("type", msg.Type).Msg("terminal: unknown control type")
			}
		case websocket.CloseMessage:
			return nil
		}
	}
}

// pumpPings pings the client every PingPeriod; each pong extends the read
// deadline set in pumpWSToPTY. WriteControl may run concurrently with the
// WriteMessage calls in pumpPTYToWS.
func (s *Session) pumpPings(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.PingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("ping pump ctx: %w", ctx.Err())
		case <-ticker.C:
			if err := s.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(pingWriteWait)); err != nil {
				return fmt.Errorf("ws ping: %w", err)
			}
		}
	}
}

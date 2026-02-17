// internal/api/v2/terminal.go
package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	terminalWriteWait  = 10 * time.Second
	terminalPongWait   = 60 * time.Second
	terminalPingPeriod = (terminalPongWait * 9) / 10 // 54s — must be < pongWait
	terminalMaxMsgSize = 32 * 1024                   // 32KB max message
)

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// Validate Origin against Host to prevent Cross-Site WebSocket Hijacking (CSWSH).
		// Browsers set Origin on WebSocket upgrade; non-browser clients (curl, wscat) may
		// omit it — we allow those through to support local tooling and API testing.
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client; auth middleware still enforces authentication
		}
		// Parse origin and compare host (scheme+host+port) to request Host header.
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// initTerminalRoutes registers the terminal WebSocket endpoint.
func (c *Controller) initTerminalRoutes() {
	c.logInfoIfEnabled("Initializing terminal routes")

	terminalGroup := c.Group.Group("/terminal")
	protectedGroup := terminalGroup.Group("", c.authMiddleware)
	protectedGroup.GET("/ws", c.HandleTerminalWS)

	c.logInfoIfEnabled("Terminal routes initialized successfully")
}

// HandleTerminalWS handles WebSocket connections for the browser terminal.
// The terminal is only available when EnableTerminal is set in config.
func (c *Controller) HandleTerminalWS(ctx echo.Context) error {
	settings := conf.Setting()
	if settings == nil || !settings.WebServer.EnableTerminal {
		return ctx.JSON(http.StatusForbidden, map[string]string{
			"error": "Terminal is disabled. Enable it in settings.",
		})
	}

	conn, err := terminalUpgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		c.logErrorIfEnabled("Failed to upgrade terminal WebSocket", logger.Error(err))
		return err
	}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(terminalMaxMsgSize)

	// Find the shell to use
	shell := findShell()

	// Start the shell in a PTY
	cmd := exec.CommandContext(c.ctx, shell) //nolint:gosec // shell path comes from findShell, not user input
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		c.logErrorIfEnabled("Failed to start terminal PTY", logger.Error(err))
		_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to start shell: "+err.Error()+"\r\n"))
		// The WebSocket connection is already hijacked; return nil to avoid
		// Echo's error handler writing an HTTP response to a hijacked conn.
		return nil
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// writeMu serializes all WebSocket writes — gorilla/websocket requires
	// at most one concurrent writer; we have both a PTY goroutine and a ping
	// goroutine that write, so they must share this mutex.
	var writeMu sync.Mutex

	// PTY → WebSocket goroutine: forwards shell output to the browser.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteWait))
				writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n])
				writeMu.Unlock()
				if writeErr != nil {
					return
				}
			}
			if readErr != nil {
				// Shell has exited or PTY was closed. Close the WebSocket so the
				// main read loop and ping goroutine exit cleanly instead of staying
				// open indefinitely (pings would otherwise prevent the read deadline
				// from ever firing).
				_ = conn.Close()
				return
			}
		}
	}()

	// Ping goroutine: sends periodic pings so the pong handler can reset the
	// read deadline. Without this, the 60-second read deadline fires after the
	// initial connection and never resets, dropping all sessions at T+60s.
	pingTicker := time.NewTicker(terminalPingPeriod)
	defer pingTicker.Stop()
	go func() {
		for {
			select {
			case <-pingTicker.C:
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteWait))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				writeMu.Unlock()
				if err != nil {
					return
				}
			case <-c.ctx.Done():
				return
			}
		}
	}()

	// WebSocket → PTY (main loop)
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
		return nil
	})

	for {
		msgType, msg, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		switch msgType {
		case websocket.TextMessage:
			// Text messages are either resize control messages (JSON) or user input.
			// Binary messages are always raw PTY data and skip this check entirely.
			if handled := handleResizeMessage(ptmx, msg); handled {
				continue
			}
			if _, err := ptmx.Write(msg); err != nil {
				// PTY write failed (shell exited); return nil because the WebSocket
				// connection is already hijacked and Echo must not write an HTTP error.
				return nil
			}
		case websocket.BinaryMessage:
			if _, err := ptmx.Write(msg); err != nil {
				return nil
			}
		}
	}

	return nil
}

// handleResizeMessage attempts to parse and apply a terminal resize message.
// Returns true if the message was a resize message (even if resize failed).
func handleResizeMessage(ptmx *os.File, msg []byte) bool {
	var resizeMsg struct {
		Type string `json:"type"`
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	if err := json.Unmarshal(msg, &resizeMsg); err != nil || resizeMsg.Type != "resize" {
		return false
	}
	if resizeMsg.Cols > 0 && resizeMsg.Rows > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{
			Cols: resizeMsg.Cols,
			Rows: resizeMsg.Rows,
		})
	}
	return true
}

// findShell returns the path to an available shell.
// Falls back to /bin/sh so exec.Command receives an absolute path rather than
// searching PATH, which could be manipulated in a compromised environment.
func findShell() string {
	for _, shell := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "/bin/sh"
}

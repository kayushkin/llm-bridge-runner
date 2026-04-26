package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kayushkin/llm-bridge/msg"
)

// Version is set at build time via -ldflags "-X main.Version=…".
var Version = "dev"

// Defaults for connection lifecycle.
const (
	dialTimeout         = 10 * time.Second
	welcomeTimeout      = 10 * time.Second
	defaultPingInterval = 30 * time.Second
	pongTimeout         = 90 * time.Second
	writeTimeout        = 10 * time.Second
	outgoingBuffer      = 256

	// Reconnect backoff envelope: starts at minBackoff, doubles to maxBackoff.
	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second
)

// Client is a long-lived runner that maintains a single WebSocket connection
// to llm-bridge-server. On disconnect it kills all in-flight subprocesses
// and reconnects with exponential backoff.
type Client struct {
	cfg      *Config
	outgoing chan *msg.RunnerMessage
	registry *SubprocessRegistry
}

// NewClient constructs a runner client. The returned Client must be Run.
func NewClient(cfg *Config) *Client {
	outgoing := make(chan *msg.RunnerMessage, outgoingBuffer)
	return &Client{
		cfg:      cfg,
		outgoing: outgoing,
		registry: NewSubprocessRegistry(outgoing),
	}
}

// Run drives the connect-and-serve loop until the context is cancelled.
// Each iteration attempts one connection; on disconnect, in-flight
// subprocesses are killed and the loop redials after a backoff.
func (c *Client) Run(ctx context.Context) {
	backoff := minBackoff
	for {
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			log.Printf("[runner] shutting down: %v", ctx.Err())
			c.registry.KillAll()
			return
		}
		log.Printf("[runner] disconnected: %v", err)
		c.registry.KillAll()

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runOnce establishes one WebSocket connection, performs the Hello/Welcome
// handshake, then runs the read+write loops until the connection dies or
// the context is cancelled. Returns the disconnect cause (nil if cancelled).
func (c *Client) runOnce(ctx context.Context) error {
	wsURL, err := buildWSURL(c.cfg.ServerURL)
	if err != nil {
		return fmt.Errorf("build ws url: %w", err)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: dialTimeout,
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.cfg.RunnerToken)

	log.Printf("[runner] dialing %s", wsURL)
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	hello := &msg.RunnerMessage{
		Type: msg.RunnerMsgHello,
		Hello: &msg.RunnerHello{
			MachineName:        c.cfg.MachineName,
			Hostname:           c.cfg.Hostname,
			OS:                 c.cfg.OS,
			Arch:               c.cfg.Arch,
			User:               c.cfg.User,
			WorkingDir:         c.cfg.WorkingDir,
			AvailableHarnesses: DetectHarnesses(),
			RunnerVersion:      Version,
		},
	}
	if err := writeJSON(conn, hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(welcomeTimeout))
	var welcome msg.RunnerMessage
	if err := conn.ReadJSON(&welcome); err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	switch welcome.Type {
	case msg.RunnerMsgWelcome:
		if welcome.Welcome == nil {
			return fmt.Errorf("welcome payload missing")
		}
		log.Printf("[runner] welcomed: machine_id=%s server=%s",
			welcome.Welcome.MachineID, welcome.Welcome.ServerVersion)
	case msg.RunnerMsgError:
		if welcome.Err != nil {
			return fmt.Errorf("server rejected hello: %s — %s",
				welcome.Err.Code, welcome.Err.Message)
		}
		return fmt.Errorf("server rejected hello (no detail)")
	default:
		return fmt.Errorf("expected welcome, got %s", welcome.Type)
	}

	pingInterval := defaultPingInterval
	if welcome.Welcome.PingIntervalSecs > 0 {
		pingInterval = time.Duration(welcome.Welcome.PingIntervalSecs) * time.Second
	}

	conn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	writeErr := make(chan error, 1)
	go c.writer(connCtx, conn, writeErr)
	go c.pinger(connCtx, conn, pingInterval)

	readErr := c.reader(conn)

	cancel()
	select {
	case <-writeErr:
	case <-time.After(writeTimeout):
	}

	return readErr
}

// reader reads RunnerMessages from the conn and dispatches them. Returns
// when the conn errors or closes. Server → runner messages: Spawn, Stdin,
// Signal, Ping, Pong (echoed). Anything unexpected is logged and ignored.
func (c *Client) reader(conn *websocket.Conn) error {
	for {
		var m msg.RunnerMessage
		if err := conn.ReadJSON(&m); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		switch m.Type {
		case msg.RunnerMsgSpawn:
			if m.Spawn == nil || m.SessionID == "" {
				log.Printf("[runner] invalid spawn: missing fields")
				continue
			}
			if err := c.registry.Spawn(m.SessionID, m.Spawn); err != nil {
				log.Printf("[runner] spawn failed (session=%s): %v", m.SessionID, err)
				c.outgoing <- &msg.RunnerMessage{
					Type:      msg.RunnerMsgExit,
					SessionID: m.SessionID,
					Exit:      &msg.RunnerExit{ExitCode: -1, Error: err.Error()},
				}
			}
		case msg.RunnerMsgStdin:
			if m.Stdin == nil || m.SessionID == "" {
				continue
			}
			sp := c.registry.Get(m.SessionID)
			if sp == nil {
				log.Printf("[runner] stdin for unknown session: %s", m.SessionID)
				continue
			}
			if err := sp.WriteLine(m.Stdin.Data); err != nil {
				log.Printf("[runner] stdin write failed (session=%s): %v", m.SessionID, err)
			}
		case msg.RunnerMsgSignal:
			if m.Signal == nil || m.SessionID == "" {
				continue
			}
			sp := c.registry.Get(m.SessionID)
			if sp == nil {
				continue
			}
			if err := sp.Signal(m.Signal.Signal); err != nil {
				log.Printf("[runner] signal failed (session=%s): %v", m.SessionID, err)
			}
		case msg.RunnerMsgPing:
			c.outgoing <- &msg.RunnerMessage{Type: msg.RunnerMsgPong}
		case msg.RunnerMsgPong:
			// Pongs reset the read deadline via SetPongHandler.
		default:
			log.Printf("[runner] unexpected message type: %s", m.Type)
		}
	}
}

// writer drains outgoing onto the WS conn until the conn dies or ctx
// cancels. Reports its terminal error on errCh.
func (c *Client) writer(ctx context.Context, conn *websocket.Conn, errCh chan<- error) {
	defer close(errCh)
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.outgoing:
			if err := writeJSON(conn, m); err != nil {
				errCh <- err
				return
			}
		}
	}
}

// pinger sends a WebSocket-level ping at the negotiated cadence. The pong
// handler installed in runOnce extends the read deadline.
func (c *Client) pinger(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// writeJSON marshals m and writes it as a single text frame with the
// configured deadline.
func writeJSON(conn *websocket.Conn, m *msg.RunnerMessage) error {
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// buildWSURL converts an http(s)://host:port base into the runner WS URL
// at /api/runner/ws. Accepts ws://, wss://, http://, https://.
func buildWSURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/runner/ws"
	return u.String(), nil
}

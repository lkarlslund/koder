// Package claudeapp implements the Claude Code stream-json transport: user
// messages and control requests go to stdin, and stdout carries the
// conversation stream plus control requests from the CLI, such as calls to an
// SDK-hosted MCP server.
package claudeapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxMessageBytes = 16 << 20

// StreamArgs are the arguments that put the CLI in bidirectional stream-json
// mode. Callers append their own options.
var StreamArgs = []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}

type Config struct {
	Executable string
	// Args is the complete argument list, normally StreamArgs plus options.
	Args []string
	Env  []string
	Dir  string
}

// Message is one line of the CLI's output stream. Control responses to this
// client's own requests are consumed internally and never appear here.
type Message struct {
	Type    string
	Subtype string
	Raw     json.RawMessage
}

// ControlRequest is a request the CLI sends to the host.
type ControlRequest struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// ControlHandler answers a control request on the reading goroutine. It
// returns handled=false to deliver the request on Messages instead, for work
// that belongs to the running turn.
type ControlHandler func(ControlRequest) (response any, handled bool, err error)

type controlResult struct {
	response json.RawMessage
	err      error
}

type Client struct {
	cfg     Config
	handler ControlHandler

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  map[string]chan controlResult
	messages chan Message
	done     chan struct{}
	runErr   error
	stderr   strings.Builder

	nextID atomic.Uint64
	write  sync.Mutex
}

func New(cfg Config, handler ControlHandler) *Client {
	if strings.TrimSpace(cfg.Executable) == "" {
		cfg.Executable = "claude"
	}
	return &Client{cfg: cfg, handler: handler, pending: map[string]chan controlResult{}, messages: make(chan Message, 1024)}
}

// Start launches the CLI and completes the initialize handshake. The returned
// response describes the account, models, and commands the CLI offers.
func (c *Client) Start(ctx context.Context, initialize map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.cmd != nil {
		c.mu.Unlock()
		return nil, errors.New("claude client already started")
	}
	cmd := exec.Command(c.cfg.Executable, c.cfg.Args...)
	cmd.Env = append(os.Environ(), c.cfg.Env...)
	cmd.Dir = c.cfg.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("open claude stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("open claude stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("open claude stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("start %s: %w", c.cfg.Executable, err)
	}
	c.cmd = cmd
	c.stdin = stdin
	c.done = make(chan struct{})
	c.mu.Unlock()
	// Output and stderr must both be drained before Wait may run.
	var drained sync.WaitGroup
	drained.Add(2)
	go func() { defer drained.Done(); c.readLoop(stdout) }()
	go func() { defer drained.Done(); c.stderrLoop(stderr) }()
	go c.waitLoop(cmd, &drained)

	request := map[string]any{"subtype": "initialize"}
	maps.Copy(request, initialize)
	response, err := c.Control(ctx, request)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize claude: %w", err)
	}
	return response, nil
}

// Control sends a control request, such as initialize or interrupt, and waits
// for its response.
func (c *Client) Control(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	key := "koder-" + strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan controlResult, 1)
	c.mu.Lock()
	if c.cmd == nil || c.runErr != nil {
		err := c.runErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("claude is not running")
		}
		return nil, err
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"type": "control_request", "request_id": key, "request": request}); err != nil {
		c.removePending(key)
		return nil, err
	}
	select {
	case got := <-ch:
		return got.response, got.err
	case <-ctx.Done():
		c.removePending(key)
		return nil, ctx.Err()
	}
}

// SendUser queues a user message. The CLI starts a turn when it is idle.
func (c *Client) SendUser(text string) error {
	return c.send(map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil,
		"session_id":         "",
	})
}

// Respond answers a control request the CLI sent. A non-nil err sends an
// error response instead of response.
func (c *Client) Respond(requestID string, response any, err error) error {
	if err != nil {
		return c.send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": requestID, "error": err.Error()}})
	}
	if response == nil {
		response = map[string]any{}
	}
	return c.send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": requestID, "response": response}})
}

// Messages carries the output stream.
func (c *Client) Messages() <-chan Message { return c.messages }

// Done is closed once the process has exited and all of its output is on
// Messages; Err then says why it exited.
func (c *Client) Done() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.done
}

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil && c.runErr == nil {
		return errors.New("claude is not running")
	}
	return c.runErr
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	cmd, stdin, done := c.cmd, c.stdin, c.done
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			return errors.New("timed out stopping claude")
		}
	}
	return nil
}

func (c *Client) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode claude message: %w", err)
	}
	data = append(data, '\n')
	c.write.Lock()
	defer c.write.Unlock()
	c.mu.Lock()
	stdin, runErr := c.stdin, c.runErr
	c.mu.Unlock()
	if runErr != nil {
		return runErr
	}
	if stdin == nil {
		return errors.New("claude is not running")
	}
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("write claude message: %w", err)
	}
	return nil
}

func (c *Client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		var head struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			// The CLI prints some fatal errors as plain text before exiting.
			c.appendStderr(string(line))
			continue
		}
		raw := json.RawMessage(append([]byte(nil), line...))
		switch head.Type {
		case "control_response":
			c.mu.Lock()
			ch := c.pending[head.Response.RequestID]
			delete(c.pending, head.Response.RequestID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if head.Response.Subtype == "error" {
				ch <- controlResult{err: fmt.Errorf("claude control error: %s", head.Response.Error)}
			} else {
				ch <- controlResult{response: head.Response.Response}
			}
			continue
		case "control_request":
			if c.handleControl(raw) {
				continue
			}
		case "keep_alive", "control_cancel_request":
			continue
		}
		c.messages <- Message{Type: head.Type, Subtype: head.Subtype, Raw: raw}
	}
	if err := scanner.Err(); err != nil {
		c.mu.Lock()
		if c.runErr == nil {
			c.runErr = fmt.Errorf("read claude output: %w", err)
		}
		cmd := c.cmd
		c.mu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

func (c *Client) handleControl(raw json.RawMessage) bool {
	var request ControlRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return true
	}
	if c.handler == nil {
		_ = c.Respond(request.RequestID, nil, errors.New("koder does not handle this control request"))
		return true
	}
	response, handled, err := c.handler(request)
	if !handled {
		return false
	}
	_ = c.Respond(request.RequestID, response, err)
	return true
}

func (c *Client) stderrLoop(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		c.appendStderr(scanner.Text())
	}
}

func (c *Client) appendStderr(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stderr.Len() < 32*1024 {
		c.stderr.WriteString(line)
		c.stderr.WriteByte('\n')
	}
}

func (c *Client) waitLoop(cmd *exec.Cmd, drained *sync.WaitGroup) {
	drained.Wait()
	err := cmd.Wait()
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	if c.runErr == nil {
		if detail := strings.TrimSpace(c.stderr.String()); detail != "" {
			c.runErr = fmt.Errorf("claude exited: %w: %s", err, detail)
		} else {
			c.runErr = fmt.Errorf("claude exited: %w", err)
		}
	}
	runErr := c.runErr
	pending := c.pending
	c.pending = map[string]chan controlResult{}
	c.stdin = nil
	done := c.done
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- controlResult{err: runErr}
	}
	close(done)
}

func (c *Client) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

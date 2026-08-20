// Package codexapp implements the Codex app-server JSONL transport.
package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxMessageBytes = 16 << 20

type Config struct {
	Executable string
	Args       []string
	CodexHome  string
	Env        []string
}

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

type response struct {
	result json.RawMessage
	err    error
}

type Client struct {
	cfg Config

	startMu sync.Mutex
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pending map[string]chan response
	subs    map[uint64]chan Message
	nextSub uint64
	done    chan struct{}
	runErr  error
	stderr  strings.Builder

	nextID atomic.Uint64
	write  sync.Mutex
}

func New(cfg Config) *Client {
	if strings.TrimSpace(cfg.Executable) == "" {
		cfg.Executable = "codex"
	}
	return &Client{cfg: cfg, pending: map[string]chan response{}, subs: map[uint64]chan Message{}}
}

func (c *Client) Start(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("codex app-server client is required")
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	c.mu.Lock()
	if c.cmd != nil && c.runErr == nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	args := append([]string(nil), c.cfg.Args...)
	if len(args) == 0 {
		args = []string{"app-server", "--stdio"}
	}
	cmd := exec.Command(c.cfg.Executable, args...)
	cmd.Env = append(os.Environ(), c.cfg.Env...)
	if home := strings.TrimSpace(c.cfg.CodexHome); home != "" {
		cmd.Env = append(cmd.Env, "CODEX_HOME="+home)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open codex stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open codex stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open codex stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.cfg.Executable, err)
	}
	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.done = make(chan struct{})
	c.runErr = nil
	c.stderr.Reset()
	c.mu.Unlock()
	go c.readLoop(stdout)
	go c.stderrLoop(stderr)
	go c.waitLoop(cmd)

	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "koder", "title": "Koder", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, &initialized); err != nil {
		_ = c.closeProcess()
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	if err := c.Notify("initialized", nil); err != nil {
		_ = c.closeProcess()
		return fmt.Errorf("notify codex initialized: %w", err)
	}
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if strings.TrimSpace(method) == "" {
		return fmt.Errorf("codex method is required")
	}
	id := c.nextID.Add(1)
	key := strconv.FormatUint(id, 10)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.cmd == nil || c.stdin == nil || c.runErr != nil {
		err := c.runErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("codex app-server is not running")
		}
		return err
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.removePending(key)
		return err
	}
	select {
	case got := <-ch:
		if got.err != nil {
			return got.err
		}
		if out == nil || len(got.result) == 0 || string(got.result) == "null" {
			return nil
		}
		if err := json.Unmarshal(got.result, out); err != nil {
			return fmt.Errorf("decode codex %s response: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(key)
		return ctx.Err()
	}
}

func (c *Client) Notify(method string, params any) error {
	if strings.TrimSpace(method) == "" {
		return fmt.Errorf("codex method is required")
	}
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

// Respond answers a request initiated by app-server, such as an approval or a
// dynamic tool call.
func (c *Client) Respond(id json.RawMessage, result any, rpcErr *RPCError) error {
	if len(id) == 0 {
		return fmt.Errorf("codex request id is required")
	}
	msg := map[string]any{"id": json.RawMessage(id)}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	return c.send(msg)
}

func (c *Client) Subscribe(buffer int) (<-chan Message, func()) {
	if buffer < 1 {
		buffer = 128
	}
	ch := make(chan Message, buffer)
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.subs[id] = ch
	c.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.subs, id)
			c.mu.Unlock()
		})
	}
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	return c.closeProcess()
}

func (c *Client) closeProcess() error {
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
			return fmt.Errorf("timed out stopping codex app-server")
		}
	}
	return nil
}

func (c *Client) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode codex message: %w", err)
	}
	data = append(data, '\n')
	c.write.Lock()
	defer c.write.Unlock()
	c.mu.Lock()
	stdin, runErr := c.stdin, c.runErr
	c.mu.Unlock()
	if stdin == nil || runErr != nil {
		if runErr != nil {
			return runErr
		}
		return errors.New("codex app-server is not running")
	}
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("write codex message: %w", err)
	}
	return nil
}

func (c *Client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			c.fail(fmt.Errorf("decode codex message: %w", err))
			return
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			key := strings.Trim(string(msg.ID), `"`)
			c.mu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ch != nil {
				if msg.Error != nil {
					ch <- response{err: msg.Error}
				} else {
					ch <- response{result: msg.Result}
				}
			}
			continue
		}
		c.broadcast(msg)
	}
	if err := scanner.Err(); err != nil {
		c.fail(fmt.Errorf("read codex output: %w", err))
	}
}

func (c *Client) stderrLoop(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		c.mu.Lock()
		if c.stderr.Len() < 32*1024 {
			c.stderr.WriteString(scanner.Text())
			c.stderr.WriteByte('\n')
		}
		c.mu.Unlock()
	}
}

func (c *Client) waitLoop(cmd *exec.Cmd) {
	err := cmd.Wait()
	c.mu.Lock()
	if c.cmd != cmd {
		c.mu.Unlock()
		return
	}
	if err == nil {
		err = io.EOF
	}
	if detail := strings.TrimSpace(c.stderr.String()); detail != "" {
		err = fmt.Errorf("codex app-server exited: %w: %s", err, detail)
	} else {
		err = fmt.Errorf("codex app-server exited: %w", err)
	}
	c.runErr = err
	c.cmd = nil
	c.stdin = nil
	pending := c.pending
	c.pending = map[string]chan response{}
	done := c.done
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: err}
	}
	if done != nil {
		close(done)
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	cmd := c.cmd
	c.runErr = err
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (c *Client) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *Client) broadcast(msg Message) {
	c.mu.Lock()
	subs := make([]chan Message, 0, len(c.subs))
	for _, ch := range c.subs {
		subs = append(subs, ch)
	}
	c.mu.Unlock()
	for _, ch := range subs {
		ch <- msg
	}
}

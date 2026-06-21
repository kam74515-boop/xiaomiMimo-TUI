// Package mcp implements a minimal Model Context Protocol (MCP) client over a
// stdio JSON-RPC transport: newline-delimited JSON-RPC 2.0 messages.
//
// The protocol logic works over any io.Reader/io.Writer (transport-injectable),
// so it can be tested against an in-process fake server without spawning a real
// MCP binary. Dial wires the same Client to a child process's stdio pipes.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// ToolDef describes a tool exposed by an MCP server.
type ToolDef struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	InputSchema map[string]any             `json:"inputSchema"`
	Raw         map[string]json.RawMessage `json:"-"`
}

// ServerInfo is returned by initialize.
type ServerInfo struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocolVersion"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message) }

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Client is a stdio JSON-RPC MCP client.
type Client struct {
	w      io.Writer
	closer io.Closer

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	closed  bool
	readErr error
	done    chan struct{}
}

// NewClient starts a client reading responses from r and writing requests to w.
// closer (optional) is closed by Close to tear down the underlying transport.
func NewClient(r io.Reader, w io.Writer, closer io.Closer) *Client {
	c := &Client{
		w:       w,
		closer:  closer,
		pending: make(map[int64]chan rpcMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

func (c *Client) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // ignore non-JSON lines (e.g. server logging)
		}
		if msg.ID == nil {
			continue // notification from server; not handled
		}
		c.mu.Lock()
		ch, ok := c.pending[*msg.ID]
		if ok {
			delete(c.pending, *msg.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.failAll(err)
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.readErr = err
	pending := c.pending
	c.pending = map[int64]chan rpcMessage{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcMessage{Error: &rpcError{Code: -1, Message: "connection closed: " + err.Error()}}
	}
	close(c.done)
}

// call sends a request and waits for the matching response.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: client closed: %w", c.readErr)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: mustRaw(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// notify sends a notification (no id, no response expected).
func (c *Client) notify(method string, params any) error {
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustRaw(params)})
}

func (c *Client) write(msg rpcMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal: %w", err)
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(data)
	return err
}

// Initialize performs the MCP handshake and sends the initialized notification.
func (c *Client) Initialize(ctx context.Context) (ServerInfo, error) {
	raw, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mimo-tui", "version": "1.0"},
	})
	if err != nil {
		return ServerInfo{}, err
	}
	var res struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	_ = json.Unmarshal(raw, &res)
	info := res.ServerInfo
	info.ProtocolVersion = res.ProtocolVersion
	// Best-effort initialized notification; servers expect it before requests.
	_ = c.notify("notifications/initialized", map[string]any{})
	return info, nil
}

// ListTools returns the tools the server exposes.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list: %w", err)
	}
	return res.Tools, nil
}

// CallTool invokes a tool and returns the concatenated text content.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("mcp: parse tools/call: %w", err)
	}
	var text string
	for _, part := range res.Content {
		if part.Type == "text" || part.Text != "" {
			text += part.Text
		}
	}
	if res.IsError {
		return text, fmt.Errorf("mcp tool reported error: %s", text)
	}
	return text, nil
}

// Close tears down the client and its transport.
func (c *Client) Close() error {
	c.mu.Lock()
	already := c.closed
	c.mu.Unlock()
	if !already {
		c.failAll(errors.New("client closed by caller"))
	}
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

// Dial spawns command as a child process and returns a Client over its stdio.
func Dial(ctx context.Context, command string, args []string, env []string) (*Client, *exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("mcp: start %q: %w", command, err)
	}
	closer := &procCloser{stdin: stdin, cmd: cmd}
	return NewClient(stdout, stdin, closer), cmd, nil
}

type procCloser struct {
	stdin io.Closer
	cmd   *exec.Cmd
}

func (p *procCloser) Close() error {
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	return nil
}

func mustRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

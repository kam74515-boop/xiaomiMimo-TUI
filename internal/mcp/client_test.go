package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeServer speaks the MCP subset over the given pipes, echoing tool calls.
func fakeServer(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = out.Write(append(b, '\n'))
	}
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		id, hasID := m["id"]
		if !hasID || id == nil {
			continue // notification
		}
		method, _ := m["method"].(string)
		switch method {
		case "initialize":
			enc(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "fake", "version": "0.1"},
			}})
		case "tools/list":
			enc(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"tools": []any{
					map[string]any{"name": "echo", "description": "echoes input", "inputSchema": map[string]any{"type": "object"}},
					map[string]any{"name": "boom", "description": "always errors"},
				},
			}})
		case "tools/call":
			params, _ := m["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			if name == "boom" {
				enc(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "kaboom"}}, "isError": true,
				}})
				continue
			}
			enc(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("echo:%v", args["msg"])}},
			}})
		default:
			enc(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	cr, cw := io.Pipe() // client -> server
	sr, sw := io.Pipe() // server -> client
	go fakeServer(cr, sw)
	c := NewClient(sr, cw, nil)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestInitializeListCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := newTestClient(t)

	info, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if info.Name != "fake" || info.ProtocolVersion != "2024-11-05" {
		t.Fatalf("server info = %+v", info)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	out, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(out, "echo:hi") {
		t.Fatalf("CallTool output = %q", out)
	}
}

func TestCallToolError(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t)
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	out, err := c.CallTool(ctx, "boom", nil)
	if err == nil {
		t.Fatalf("expected error from isError tool, got out=%q", out)
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("error should carry server text: %v", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t)
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			out, err := c.CallTool(ctx, "echo", map[string]any{"msg": i})
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(out, fmt.Sprintf("echo:%d", i)) {
				errs <- fmt.Errorf("response %q did not match request %d (id demux bug)", out, i)
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent call: %v", err)
		}
	}
}

func TestClosePendingFails(t *testing.T) {
	// A client whose transport closes should fail in-flight/next calls, not hang.
	sr, _ := io.Pipe()
	_, cw := io.Pipe()
	c := NewClient(sr, cw, nil)
	_ = c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.ListTools(ctx); err == nil {
		t.Fatal("ListTools on a closed client should error, not hang")
	}
}

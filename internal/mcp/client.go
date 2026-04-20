// Package mcp implements a minimal MCP (Model Context Protocol) client.
// Supports stdio transport (JSON-RPC 2.0 over stdin/stdout) which covers the
// vast majority of MCP servers. SSE/streamable-HTTP transports can be added
// in a follow-up phase.
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
	"sync/atomic"
)

// ToolDef is a server-advertised tool.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client is an MCP stdio client.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan rpcResponse
	listeners []func(notification)

	tools []ToolDef
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// StartStdio launches an MCP server process and performs the initialize
// handshake. Returns an initialized Client.
func StartStdio(ctx context.Context, cmdPath string, args []string, env map[string]string) (*Client, error) {
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		cmd:     cmd,
		stdin:   in,
		stdout:  out,
		pending: make(map[int64]chan rpcResponse),
	}
	go c.readLoop()
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.loadTools(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Close terminates the server process.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

// Tools returns the cached list of advertised tools.
func (c *Client) Tools() []ToolDef {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolDef, len(c.tools))
	copy(out, c.tools)
	return out
}

// Call invokes a tool on the server.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	raw, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	out := ""
	for _, p := range parsed.Content {
		if p.Type == "text" {
			out += p.Text
		}
	}
	return out, nil
}

func (c *Client) initialize(ctx context.Context) error {
	_, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"roots": map[string]any{"listChanged": false},
		},
		"clientInfo": map[string]any{"name": "nanobot-go", "version": "0.1.0"},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *Client) loadTools(ctx context.Context) error {
	raw, err := c.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var parsed struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	c.mu.Lock()
	c.tools = parsed.Tools
	c.mu.Unlock()
	return nil
}

func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.writeMessage(req); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	return c.writeMessage(msg)
}

func (c *Client) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var env struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			continue
		}
		if env.ID != nil {
			resp := rpcResponse{ID: *env.ID, Result: env.Result, Error: env.Error}
			c.mu.Lock()
			ch, ok := c.pending[*env.ID]
			c.mu.Unlock()
			if ok {
				ch <- resp
			}
			continue
		}
		// notification; ignore for MVP
	}
}

// ErrNotImplemented is returned for unsupported transports.
var ErrNotImplemented = errors.New("mcp transport not implemented")

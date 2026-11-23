// Package toolmcp gives the model runtime the tools of MCP servers. Each
// server's tools become ordinary tools; a call opens (or reuses) that
// thread's connection to the server and runs the tool over MCP. Images in a
// result are kept as tool output files and attached for the model.
package toolmcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

// Connector opens a connection to the server on behalf of a thread. An
// empty convID connects only to list the tools.
type Connector func(ctx context.Context, convID string) (*mcp.Client, error)

// Server is one MCP server for the model runtime.
type Server struct {
	Name string
	// Prefix is put before each tool name ("conversation_"), so names from
	// different servers cannot collide; "" keeps them.
	Prefix  string
	Connect Connector
}

// Pool holds each thread's open connections.
type Pool struct {
	mu    sync.Mutex
	conns map[string]*mcp.Client // convID + "\x00" + server
}

// NewPool returns an empty pool.
func NewPool() *Pool { return &Pool{conns: map[string]*mcp.Client{}} }

func (p *Pool) client(ctx context.Context, s Server, convID string) (*mcp.Client, error) {
	key := convID + "\x00" + s.Name
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[key]; ok {
		select {
		case <-c.Done():
			delete(p.conns, key)
		default:
			return c, nil
		}
	}
	c, err := s.Connect(ctx, convID)
	if err != nil {
		return nil, err
	}
	p.conns[key] = c
	return c, nil
}

// Release closes a thread's connections, when the thread is deleted.
func (p *Pool) Release(convID string) {
	p.mu.Lock()
	var closing []*mcp.Client
	for key, c := range p.conns {
		if strings.HasPrefix(key, convID+"\x00") {
			closing = append(closing, c)
			delete(p.conns, key)
		}
	}
	p.mu.Unlock()
	for _, c := range closing {
		_ = c.Close()
	}
}

// Close closes every connection.
func (p *Pool) Close() {
	p.mu.Lock()
	conns := p.conns
	p.conns = map[string]*mcp.Client{}
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Tools lists the server's tools and wraps each one. outputs stores image
// results.
func Tools(ctx context.Context, s Server, pool *Pool, outputs drepo.ToolOutput) ([]dservice.Tool, error) {
	probe, err := s.Connect(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", s.Name, err)
	}
	defer probe.Close()
	listed, err := probe.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", s.Name, err)
	}
	tools := make([]dservice.Tool, 0, len(listed))
	for _, t := range listed {
		sch, err := valueobject.NewToolSchema(s.Prefix+t.Name, t.Description, t.InputSchema, true, enum.ToolPermissionAgent)
		if err != nil {
			return nil, fmt.Errorf("mcp server %s tool %s: %w", s.Name, t.Name, err)
		}
		tools = append(tools, &tool{server: s, name: t.Name, schema: sch, pool: pool, outputs: outputs})
	}
	return tools, nil
}

type tool struct {
	server  Server
	name    string
	schema  valueobject.ToolSchema
	pool    *Pool
	outputs drepo.ToolOutput
}

func (t *tool) Schema() valueobject.ToolSchema { return t.schema }

func (t *tool) Execute(ctx context.Context, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	convID := dservice.ConversationID(ctx)
	if convID == "" {
		return dservice.ToolResult{}, errors.New("this tool needs a thread")
	}
	client, err := t.pool.client(ctx, t.server, convID)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	args := strings.TrimSpace(call.Input())
	if args == "" {
		args = "{}"
	}
	res, err := client.Call(ctx, t.name, []byte(args))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	return toolResult(ctx, t.outputs, convID, call.ID(), res)
}

// toolResult turns an MCP result into a tool result: text, or the error
// the model reads; images are kept as files of the thread and attached.
func toolResult(ctx context.Context, outputs drepo.ToolOutput, convID, callID string, res mcp.Result) (dservice.ToolResult, error) {
	text := res.Text()
	if res.IsError {
		if text == "" {
			text = "the tool failed"
		}
		return dservice.ToolResult{}, errors.New(text)
	}
	out := dservice.ToolResult{Text: text}
	for i, c := range res.Content {
		if c.Type != "image" || c.Data == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(c.Data)
		mime := c.MimeType
		if mime == "" {
			mime = "image/png"
		}
		var att valueobject.Attachment
		if err == nil {
			att, err = outputs.Keep(ctx, convID, fmt.Sprintf("%s-%d", callID, i), mime, raw)
		}
		if err != nil {
			out.Text += "\n(an image could not be kept: " + err.Error() + ")"
			continue
		}
		out.Attachments = append(out.Attachments, att)
	}
	if out.Text == "" && len(out.Attachments) == 0 {
		out.Text = "ok"
	}
	return out, nil
}

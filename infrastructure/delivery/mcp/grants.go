package deliverymcp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Environment the relay child reads.
const (
	EnvURL   = "NUSASHELL_MCP_URL"
	EnvToken = "NUSASHELL_MCP_TOKEN"
)

// Grants maps per-thread tokens to threads. A token lets an agent's relay
// call the built-in servers as that thread and nothing else. Tokens live
// in memory: agents are children of this process and die with it.
type Grants struct {
	mu     sync.Mutex
	byConv map[string]string
	byTok  map[string]string
}

// NewGrants returns an empty grant table.
func NewGrants() *Grants { return &Grants{byConv: map[string]string{}, byTok: map[string]string{}} }

// Token is the thread's token, minted on first use.
func (g *Grants) Token(convID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if tok, ok := g.byConv[convID]; ok {
		return tok
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	tok := hex.EncodeToString(raw)
	g.byConv[convID] = tok
	g.byTok[tok] = convID
	return tok
}

// Thread resolves a token.
func (g *Grants) Thread(token string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byTok[token]
	return id, ok
}

// Revoke drops a deleted thread's token.
func (g *Grants) Revoke(convID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if tok, ok := g.byConv[convID]; ok {
		delete(g.byTok, tok)
		delete(g.byConv, convID)
	}
}

// Launch tells agents how to start the built-in servers: this binary, run
// as `mcp run <server>`, relaying to the server at URL with the thread's
// token.
type Launch struct {
	Exec    string // this binary
	URL     string // this server, as the relay reaches it
	Grants  *Grants
	Servers []string
}

// For lists the servers an agent gets in a thread.
func (l Launch) For(convID string) []dservice.MCPServer {
	out := make([]dservice.MCPServer, 0, len(l.Servers))
	tok := l.Grants.Token(convID)
	for _, name := range l.Servers {
		out = append(out, dservice.MCPServer{
			Name: name, Command: l.Exec, Args: []string{"mcp", "run", name},
			Env: map[string]string{EnvURL: l.URL, EnvToken: tok},
		})
	}
	return out
}

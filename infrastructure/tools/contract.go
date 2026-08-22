// Plugin usage-contract layer: reading contract files declared via
// manifest contract.entry, caching them by mtime+size, and tracking
// per-conversation reads so mcp_call can enforce the
// plugin_contract_mode setting (off/hint/require).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nusashell/application"
	"nusashell/domain"
)

// maxContractChars bounds a single contract body. Larger files are
// truncated (meta.truncated=true) so one oversized contract cannot flood
// the model context.
const maxContractChars = 4096

// ContractSource supplies raw contract contents for installed plugins.
type ContractSource interface {
	// ReadContract returns (content, truncated, error) for the plugin.
	ReadContract(p *domain.Plugin) (string, bool, error)
}

// FileContractReader reads <InstallPath>/<contract.entry> from disk with an
// mtime+size cache. A missing file or undeclared contract yields a
// descriptive error.
type FileContractReader struct {
	mu    sync.Mutex
	cache map[string]contractCacheEntry
}

type contractCacheEntry struct {
	content   string
	truncated bool
	size      int64
	modTime   int64
}

// NewFileContractReader creates an empty contract cache.
func NewFileContractReader() *FileContractReader {
	return &FileContractReader{cache: map[string]contractCacheEntry{}}
}

// ReadContract implements ContractSource.
func (r *FileContractReader) ReadContract(p *domain.Plugin) (string, bool, error) {
	entry := p.Manifest.ContractEntry()
	if entry == "" {
		return "", false, fmt.Errorf("plugin %q declares no contract", p.Manifest.ID)
	}
	path := filepath.Join(p.InstallPath, filepath.FromSlash(entry))
	info, err := os.Stat(path)
	if err != nil {
		return "", false, fmt.Errorf("contract %q not found in plugin %q", entry, p.Manifest.ID)
	}
	key := p.Manifest.ID + ":" + entry
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[key]; ok && c.size == info.Size() && c.modTime == info.ModTime().UnixNano() {
		return c.content, c.truncated, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read contract %q: %w", entry, err)
	}
	content := strings.TrimSpace(string(raw))
	truncated := false
	if len(content) > maxContractChars {
		content = content[:maxContractChars]
		truncated = true
	}
	r.cache[key] = contractCacheEntry{
		content: content, truncated: truncated,
		size: info.Size(), modTime: info.ModTime().UnixNano(),
	}
	return content, truncated, nil
}

// contractGate remembers which plugins have had their contract read per
// conversation so mcp_call can enforce require mode. Calls outside a
// conversation share the "" key. The map is deliberately coarse; if it
// ever grows past contractGateMaxConversations it is reset wholesale —
// worst case agents re-read contracts, never that the gate silently opens.
const contractGateMaxConversations = 2048

type contractGate struct {
	mu   sync.Mutex
	read map[string]map[string]bool // conversationID -> pluginID -> read
}

func newContractGate() *contractGate {
	return &contractGate{read: map[string]map[string]bool{}}
}

func (g *contractGate) mark(ctx context.Context, pluginIDs ...string) {
	cid := application.ConversationIDFromContext(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.read[cid]; !ok && len(g.read) >= contractGateMaxConversations {
		g.read = map[string]map[string]bool{}
	}
	set := g.read[cid]
	if set == nil {
		set = map[string]bool{}
		g.read[cid] = set
	}
	for _, id := range pluginIDs {
		set[id] = true
	}
}

func (g *contractGate) hasRead(ctx context.Context, pluginID string) bool {
	cid := application.ConversationIDFromContext(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.read[cid][pluginID]
}

// gate lazily initializes the per-Toolbox gate (zero value usable because
// of sync.Once).
func (t *Toolbox) gate() *contractGate {
	t.contractsGateOnce.Do(func() { t.contractsGate = newContractGate() })
	return t.contractsGate
}

// contractMode resolves the effective enforcement mode. Missing settings or
// unknown values fall back to the factory default (hint) so a typo can't
// silently disable the gate.
func (t *Toolbox) contractMode() string {
	if t.Settings == nil {
		return domain.PluginContractHint
	}
	switch s := t.Settings.Get().PluginContractMode; s {
	case domain.PluginContractOff, domain.PluginContractHint, domain.PluginContractRequire:
		return s
	default:
		return domain.PluginContractHint
	}
}

// contractCheck enforces plugin_contract_mode for a call to a
// contract-declaring plugin. Returns (advisory, error): advisory is appended
// to successful results in hint mode; a non-nil error rejects the call
// (require mode, contract not yet read this conversation).
func (t *Toolbox) contractCheck(ctx context.Context, p *domain.Plugin) (string, error) {
	if p.Manifest.ContractEntry() == "" {
		return "", nil // plugin declares no contract — never gated
	}
	switch t.contractMode() {
	case domain.PluginContractOff:
		return "", nil
	case domain.PluginContractRequire:
		if !t.gate().hasRead(ctx, p.Manifest.ID) {
			entry := p.Manifest.ContractEntry()
			return "", fmt.Errorf("CONTRACT_REQUIRED: plugin %q declares a usage contract (%s). Call contract_read with {\"id\":\"%s\"} first, then retry this call", p.Manifest.ID, entry, p.Manifest.ID)
		}
		return "", nil
	default: // hint
		if t.gate().hasRead(ctx, p.Manifest.ID) {
			return "", nil
		}
		return "NOTE: plugin " + p.Manifest.ID + " has an unread usage contract (" + p.Manifest.ContractEntry() + ") — call contract_read before relying on its behavior", nil
	}
}

// execContractRead implements the contract_read tool. Accepts id=<plugin-id>
// or id=all. Marks the contract as read for the current conversation.
func (t *Toolbox) execContractRead(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Contracts == nil || t.Plugins == nil {
		return "", fmt.Errorf("contract reading is not available")
	}
	var args struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(argsJSON, &args)
	plugins, err := t.Plugins.List()
	if err != nil {
		return "", fmt.Errorf("contract_read: %w", err)
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return "", fmt.Errorf("id is required (plugin id or 'all')")
	}
	var targets []*domain.Plugin
	for _, p := range plugins {
		if p.Manifest.ContractEntry() == "" {
			continue
		}
		if id == "all" || p.Manifest.ID == id {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		if id != "" && id != "all" {
			return "", fmt.Errorf("plugin id %q not found or declares no contract; use mcp_list to see configured servers", id)
		}
		return yamlBlock(map[string]any{"count": 0}), nil
	}
	var marked []string
	var bodies []string
	tokens := 0
	truncatedAny := false
	for _, p := range targets {
		content, truncated, err := t.Contracts.ReadContract(p)
		if err != nil {
			return "", err
		}
		marked = append(marked, p.Manifest.ID)
		bodies = append(bodies, "# "+p.Manifest.ID+" v"+p.Manifest.Version+"\n\n"+content)
		tokens += len(content) / 4
		truncatedAny = truncatedAny || truncated
	}
	t.gate().mark(ctx, marked...)
	meta := map[string]any{"ids": marked, "tokens": tokens}
	if truncatedAny {
		meta["truncated"] = true
	}
	return yamlMD(meta, strings.Join(bodies, "\n\n---\n\n")), nil
}

package domain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Project memory is a per-workspace, skill-compatible store of anchored
// markdown entries. It is independent of user memory (primary.md + fragments).
// On-disk layout: {base}/{key}/{kind}.md with optional archive/{kind}.md.

const (
	ProjectMemoryDirName    = "memory_project"
	ProjectIndexCharCap     = PrimaryCharCap
	ProjectPatternThreshold = 3
)

// Canonical project-memory kind files (filename stems). Writes are limited
// to this set so user-profile leftovers cannot land in project memory.
const (
	ProjectKindIndex      = "index"
	ProjectKindGuardrails = "guardrails"
	ProjectKindRoadmap    = "roadmap"
	ProjectKindPlaybook   = "playbook"
	ProjectKindDevAccess  = "dev-access"
	ProjectKindDecisions  = "decisions"
	ProjectKindDebug      = "debug"
	ProjectKindValidation = "validation"
	ProjectKindTouchMap   = "touch-map"
	ProjectKindPatterns   = "patterns"
)

// ProjectKindReadOrder is the memory-list.sh priority for live files.
var ProjectKindReadOrder = []string{
	ProjectKindGuardrails,
	ProjectKindIndex,
	ProjectKindRoadmap,
	ProjectKindPlaybook,
	ProjectKindDevAccess,
	ProjectKindTouchMap,
	ProjectKindDecisions,
	ProjectKindDebug,
	ProjectKindValidation,
	ProjectKindPatterns,
}

// ProjectLinkRelations is the typed LINKS vocabulary from memory-lint.sh.
var ProjectLinkRelations = map[string]bool{
	"validated_by":   true,
	"procedure":      true,
	"constrained_by": true,
	"explained_by":   true,
	"depends_on":     true,
	"blocks":         true,
	"related_to":     true,
}

var projectKindSet = map[string]bool{
	ProjectKindIndex:      true,
	ProjectKindGuardrails: true,
	ProjectKindRoadmap:    true,
	ProjectKindPlaybook:   true,
	ProjectKindDevAccess:  true,
	ProjectKindDecisions:  true,
	ProjectKindDebug:      true,
	ProjectKindValidation: true,
	ProjectKindTouchMap:   true,
	ProjectKindPatterns:   true,
}

// File kind → KIND field (INDEX, GUARDRAIL, DEV_ACCESS, …).
var projectFileKindToEntryKind = map[string]string{
	ProjectKindIndex:      "INDEX",
	ProjectKindGuardrails: "GUARDRAIL",
	ProjectKindRoadmap:    "ROADMAP",
	ProjectKindPlaybook:   "PLAYBOOK",
	ProjectKindDevAccess:  "DEV_ACCESS",
	ProjectKindDecisions:  "DECISION",
	ProjectKindDebug:      "DEBUG",
	ProjectKindValidation: "VALIDATION",
	ProjectKindTouchMap:   "TOUCH_MAP",
	ProjectKindPatterns:   "PATTERN",
}

// File kind → required ID prefix.
var projectKindIDPrefix = map[string]string{
	ProjectKindIndex:      "IDX-",
	ProjectKindGuardrails: "G-",
	ProjectKindRoadmap:    "R-",
	ProjectKindPlaybook:   "PB-",
	ProjectKindDevAccess:  "DEV-",
	ProjectKindDecisions:  "D-",
	ProjectKindDebug:      "BUG-",
	ProjectKindValidation: "V-",
	ProjectKindTouchMap:   "T-",
	ProjectKindPatterns:   "P-",
}

var (
	projectForbiddenFiles = map[string]bool{
		"preferences":  true,
		"user-profile": true,
	}
	projectForbiddenKinds = map[string]bool{
		"user-profile": true,
		"user_profile": true,
		"preferences":  true,
	}
	kebabTopicRe  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	devAccessIDRe = regexp.MustCompile(`^DEV-[a-z0-9][a-z0-9-]*$`)
)

// ProjectMemoryEntry is one anchored block in a kind file.
type ProjectMemoryEntry struct {
	ID       string
	Kind     string // KIND field (INDEX, DEBUG, …)
	FileKind string // filename stem (index, debug, …)
	Scope    string
	Topics   []string
	Links    []ProjectMemoryLink
	Fields   map[string]string // remaining KEY: value lines, first wins
	Body     string            // full anchored block including BEGIN/END
	File     string            // relative display path (debug.md or archive/debug.md)
	Archived bool
}

// ProjectMemoryLink is a typed one-way edge stored as relation:TARGET_ID.
type ProjectMemoryLink struct {
	Relation string
	Target   string
}

// ProjectMemoryQuery mirrors memory-query.sh selectors (AND-combined).
type ProjectMemoryQuery struct {
	Topic   string
	Kind    string
	Related string
	ID      string
	Archive bool
	Full    bool
	Limit   int
}

// ProjectMemoryHit is one query result.
type ProjectMemoryHit struct {
	ID    string
	Kind  string // lowercase kebab display kind
	File  string
	Scope string
	Body  string // set when Full is true
}

// ProjectMemoryLintProblem is one memory-lint finding.
type ProjectMemoryLintProblem struct {
	File    string
	Message string
}

// ProjectIndexExtract is the compact hydration payload from IDX-project.
type ProjectIndexExtract struct {
	Purpose      string `json:"purpose,omitempty"`
	Locks        string `json:"locks,omitempty"`
	CurrentState string `json:"current_state,omitempty"`
	Routes       string `json:"routes,omitempty"`
}

// ProjectMemoryKey sanitizes a workspace path the same way memory-key.sh does.
func ProjectMemoryKey(workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "unknown-project"
	}
	// A path made only of separators has no workspace component to key.
	if strings.Trim(workspacePath, "/\\") == "" {
		return "unknown-project"
	}
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}
	abs = filepath.ToSlash(filepath.Clean(abs))
	key := strings.TrimPrefix(abs, "/")
	key = strings.ToLower(key)
	var b strings.Builder
	b.Grow(len(key))
	prevDash := false
	for _, r := range key {
		switch {
		case r == '/' || r == ':' || r == ' ':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			if r == '-' {
				if prevDash {
					continue
				}
				prevDash = true
			} else {
				prevDash = false
			}
			b.WriteRune(r)
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	key = strings.Trim(b.String(), "-")
	if key == "" {
		return "unknown-project"
	}
	return key
}

// NormalizeProjectKindFile maps a kind argument to a filename stem
// (memory-path.sh: lower, _ → -, decision → decisions, sanitize).
func NormalizeProjectKindFile(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "_", "-")
	if kind == "decision" {
		kind = "decisions"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range kind {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ProjectEntryKind returns the KIND field for a file stem (GUARDRAIL for guardrails).
func ProjectEntryKind(fileKind string) string {
	fileKind = NormalizeProjectKindFile(fileKind)
	if k, ok := projectFileKindToEntryKind[fileKind]; ok {
		return k
	}
	return strings.ToUpper(strings.ReplaceAll(fileKind, "-", "_"))
}

// ProjectKindIDPrefix returns the required ID prefix for a file kind.
func ProjectKindIDPrefix(fileKind string) string {
	return projectKindIDPrefix[NormalizeProjectKindFile(fileKind)]
}

// IsCanonicalProjectKind reports whether fileKind is a writable kind.
func IsCanonicalProjectKind(fileKind string) bool {
	return projectKindSet[NormalizeProjectKindFile(fileKind)]
}

// RejectProjectUserKind reports why a kind/file is forbidden as project memory.
// Empty string means the write is structurally allowed (canonical check is separate).
func RejectProjectUserKind(kind string) string {
	n := NormalizeProjectKindFile(kind)
	if projectForbiddenFiles[n] || projectForbiddenKinds[n] || n == "user-profile" {
		return "project memory rejects user-profile facts; use the memory tool (primary/fragments)"
	}
	entry := strings.ToUpper(strings.ReplaceAll(n, "-", "_"))
	if entry == "USER_PROFILE" {
		return "project memory rejects user-profile facts; use the memory tool (primary/fragments)"
	}
	return ""
}

// ProjectKindIDValid reports whether id uses the required prefix for fileKind.
func ProjectKindIDValid(fileKind, id string) bool {
	prefix := ProjectKindIDPrefix(fileKind)
	if prefix == "" {
		return false
	}
	return strings.HasPrefix(id, prefix)
}

// ParseProjectMemoryEntries splits a kind file into anchored entries.
func ParseProjectMemoryEntries(raw, displayFile, fileKind string, archived bool) []ProjectMemoryEntry {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	var out []ProjectMemoryEntry
	var cur []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, "### BEGIN_ENTRY: ") {
			in = true
			cur = []string{line}
			continue
		}
		if in {
			cur = append(cur, line)
			if strings.HasPrefix(line, "### END_ENTRY: ") {
				body := strings.Join(cur, "\n")
				if !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
				out = append(out, parseOneProjectEntry(body, displayFile, fileKind, archived))
				in = false
				cur = nil
			}
		}
	}
	return out
}

func parseOneProjectEntry(body, displayFile, fileKind string, archived bool) ProjectMemoryEntry {
	e := ProjectMemoryEntry{
		Body:     body,
		File:     displayFile,
		FileKind: fileKind,
		Archived: archived,
		Fields:   map[string]string{},
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "### ") {
			continue
		}
		key, val, ok := splitField(line)
		if !ok {
			continue
		}
		switch key {
		case "ID":
			if e.ID == "" {
				e.ID = val
			}
		case "KIND":
			if e.Kind == "" {
				e.Kind = val
			}
		case "SCOPE":
			if e.Scope == "" {
				e.Scope = val
			}
		case "TOPICS":
			if len(e.Topics) == 0 {
				e.Topics = parseBracketList(val)
			}
		case "LINKS":
			if len(e.Links) == 0 {
				e.Links = parseLinks(val)
			}
		default:
			if _, exists := e.Fields[key]; !exists {
				e.Fields[key] = val
			}
		}
	}
	return e
}

func splitField(line string) (key, val string, ok bool) {
	if line == "" || strings.HasPrefix(line, "###") {
		return "", "", false
	}
	i := strings.Index(line, ": ")
	if i <= 0 {
		return "", "", false
	}
	key = line[:i]
	for _, r := range key {
		if r != '_' && !unicode.IsUpper(r) && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "", "", false
		}
	}
	if key != strings.ToUpper(key) {
		// KIND fields are SCREAMING_SNAKE. Allow mixed only if all-caps tokens.
		allCaps := true
		for _, r := range key {
			if unicode.IsLetter(r) && !unicode.IsUpper(r) {
				allCaps = false
				break
			}
		}
		if !allCaps {
			return "", "", false
		}
	}
	return key, strings.TrimSpace(line[i+2:]), true
}

func parseBracketList(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseLinks(raw string) []ProjectMemoryLink {
	items := parseBracketList(raw)
	out := make([]ProjectMemoryLink, 0, len(items))
	for _, item := range items {
		rel, target, ok := strings.Cut(item, ":")
		if !ok || rel == "" || target == "" {
			continue
		}
		out = append(out, ProjectMemoryLink{Relation: rel, Target: target})
	}
	return out
}

// MatchProjectMemoryQuery applies AND selectors the way memory-query.sh does.
func MatchProjectMemoryQuery(entries []ProjectMemoryEntry, q ProjectMemoryQuery) []ProjectMemoryHit {
	wantedKind := strings.TrimSpace(q.Kind)
	if wantedKind != "" {
		wantedKind = ProjectEntryKind(wantedKind)
	}
	wantedTopic := strings.ToLower(strings.TrimSpace(q.Topic))
	wantedID := strings.TrimSpace(q.ID)
	wantedRelated := strings.TrimSpace(q.Related)

	outbound := map[string]bool{}
	if wantedRelated != "" {
		for _, e := range entries {
			if e.ID == wantedRelated {
				for _, l := range e.Links {
					outbound[l.Target] = true
				}
			}
		}
	}

	var hits []ProjectMemoryHit
	for _, e := range entries {
		if wantedTopic != "" {
			ok := false
			for _, t := range e.Topics {
				if strings.EqualFold(t, wantedTopic) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if wantedKind != "" && strings.ToUpper(e.Kind) != wantedKind {
			continue
		}
		if wantedRelated != "" {
			if e.ID == wantedRelated {
				continue
			}
			linksTo := false
			for _, l := range e.Links {
				if l.Target == wantedRelated {
					linksTo = true
					break
				}
			}
			if !linksTo && !outbound[e.ID] {
				continue
			}
		}
		if wantedID != "" && e.ID != wantedID {
			continue
		}
		displayKind := strings.ToLower(e.Kind)
		displayKind = strings.ReplaceAll(displayKind, "_", "-")
		hit := ProjectMemoryHit{
			ID:    e.ID,
			Kind:  displayKind,
			File:  e.File,
			Scope: e.Scope,
		}
		if q.Full {
			hit.Body = strings.TrimRight(e.Body, "\n") + "\n"
		}
		hits = append(hits, hit)
		if q.Limit > 0 && len(hits) >= q.Limit {
			break
		}
	}
	return hits
}

// WrapProjectEntry ensures content is an anchored block for id.
func WrapProjectEntry(id, content string) string {
	content = strings.TrimSpace(content)
	begin := "### BEGIN_ENTRY: " + id + " ###"
	end := "### END_ENTRY: " + id + " ###"
	if strings.HasPrefix(content, "### BEGIN_ENTRY: ") {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content
	}
	if !strings.Contains(content, "\nID: ") && !strings.HasPrefix(content, "ID: ") {
		content = "ID: " + id + "\n" + content
	}
	return begin + "\n" + content + "\n" + end + "\n"
}

// ExtractProjectEntryID returns the ID: field or the BEGIN_ENTRY token.
func ExtractProjectEntryID(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "ID: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ID: "))
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "### BEGIN_ENTRY: ") {
			s := strings.TrimPrefix(line, "### BEGIN_ENTRY: ")
			s = strings.TrimSuffix(s, " ###")
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// CompactProjectIndex builds the hydration extract from an IDX-project entry.
func CompactProjectIndex(e ProjectMemoryEntry) ProjectIndexExtract {
	x := ProjectIndexExtract{
		Purpose:      e.Fields["PURPOSE"],
		Locks:        e.Fields["LOCKS"],
		CurrentState: e.Fields["CURRENT_STATE"],
		Routes:       e.Fields["ROUTES"],
	}
	for len(x.Purpose)+len(x.Locks)+len(x.CurrentState)+len(x.Routes) > ProjectIndexCharCap {
		if len(x.CurrentState) > 80 {
			x.CurrentState = strings.TrimSpace(x.CurrentState[:len(x.CurrentState)*3/4]) + "…"
			continue
		}
		if len(x.Routes) > 40 {
			x.Routes = strings.TrimSpace(x.Routes[:len(x.Routes)*3/4]) + "…"
			continue
		}
		break
	}
	return x
}

// IsKebabTopic reports whether topic matches the lint vocabulary.
func IsKebabTopic(topic string) bool {
	return kebabTopicRe.MatchString(topic)
}

// IsDevAccessID reports whether id matches DEV-lowercase-kebab.
func IsDevAccessID(id string) bool {
	return devAccessIDRe.MatchString(id)
}

// ResolveProjectMemoryBase returns the directory that holds {key}/ folders.
// Empty override uses {dataDir}/memory_project. A "~" prefix expands to home.
func ResolveProjectMemoryBase(dataDir, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		if expanded, err := ExpandHomeDir(override); err == nil {
			override = expanded
		}
		if filepath.IsAbs(override) {
			return filepath.Clean(override)
		}
	}
	return filepath.Join(dataDir, ProjectMemoryDirName)
}

// ExpandHomeDir expands a leading ~/ to the current user's home directory.
func ExpandHomeDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || path == "~/" {
		home, err := osUserHomeDir()
		if err != nil {
			return path, err
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := osUserHomeDir()
		if err != nil {
			return path, err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// osUserHomeDir is a seam so tests can stub the home directory.
var osUserHomeDir = os.UserHomeDir

// HasProjectMemorySelector reports whether a query has at least one retrieval key.
func HasProjectMemorySelector(q ProjectMemoryQuery) bool {
	return strings.TrimSpace(q.Topic) != "" ||
		strings.TrimSpace(q.Kind) != "" ||
		strings.TrimSpace(q.Related) != "" ||
		strings.TrimSpace(q.ID) != ""
}

// NormalizePatternKey sanitizes PATTERN_KEY the way memory-pattern-track.sh does.
func NormalizePatternKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	prevDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			prevDash = false
		case r == '-':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	key := strings.Trim(b.String(), "-")
	if key == "none" || key == "n-a" {
		return ""
	}
	return key
}

// PatternEntryID builds P-{kind}-{key} truncated to 56 chars after the kind-key slug.
func PatternEntryID(sourceKind, patternKey string) string {
	slug := sourceKind + "-" + patternKey
	if len(slug) > 56 {
		slug = slug[:56]
	}
	return "P-" + slug
}

// ProjectLineCap is the live-file line cap from memory-lint.sh.
func ProjectLineCap(fileKind string) int {
	switch NormalizeProjectKindFile(fileKind) {
	case ProjectKindGuardrails:
		return 300
	case ProjectKindIndex:
		return 160
	case ProjectKindRoadmap, ProjectKindValidation:
		return 300
	case ProjectKindDevAccess:
		return 200
	default:
		return 400
	}
}

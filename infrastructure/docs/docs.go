// Package docs provides the product documentation corpus the agent can read
// through docs_list / docs_search / docs_read. The corpus is embedded at
// build time; a user-supplied directory may extend it.
package docs

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nusashell/application"
)

//go:embed corpus/*.md
var corpusFS embed.FS

// Source implements application.DocsSource.
type Source struct {
	extraDir string // optional user docs directory, may be ""
	docs     []docEntry
}

type docEntry struct {
	id      string
	title   string
	path    string
	content string
}

// New builds the corpus index. extraDir, when non-empty, adds *.md files
// from that directory (id = base name without extension).
func New(extraDir string) (*Source, error) {
	s := &Source{extraDir: extraDir}
	entries, err := corpusFS.ReadDir("corpus")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := corpusFS.ReadFile("corpus/" + e.Name())
		if err != nil {
			return nil, err
		}
		s.docs = append(s.docs, docEntry{
			id:      strings.TrimSuffix(e.Name(), ".md"),
			title:   titleOf(b),
			path:    "embedded:" + e.Name(),
			content: string(b),
		})
	}
	if extraDir != "" {
		files, err := os.ReadDir(extraDir)
		if err == nil {
			for _, e := range files {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				b, err := os.ReadFile(filepath.Join(extraDir, e.Name()))
				if err != nil {
					continue
				}
				s.docs = append(s.docs, docEntry{
					id:      strings.TrimSuffix(e.Name(), ".md"),
					title:   titleOf(b),
					path:    "user:" + e.Name(),
					content: string(b),
				})
			}
		}
	}
	sort.Slice(s.docs, func(i, j int) bool { return s.docs[i].id < s.docs[j].id })
	return s, nil
}

func titleOf(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "#"), " "))
		}
	}
	return "Untitled"
}

func (s *Source) List() []application.DocMeta {
	out := make([]application.DocMeta, 0, len(s.docs))
	for _, d := range s.docs {
		out = append(out, application.DocMeta{ID: d.id, Title: d.title, Path: d.path})
	}
	return out
}

func (s *Source) Search(query string, limit int) []application.DocHit {
	q := strings.ToLower(strings.TrimSpace(query))
	var hits []application.DocHit
	for _, d := range s.docs {
		lower := strings.ToLower(d.content)
		idx := strings.Index(lower, q)
		if q == "" || idx >= 0 {
			snippet := ""
			if idx >= 0 {
				start := idx - 80
				if start < 0 {
					start = 0
				}
				end := idx + len(q) + 120
				if end > len(d.content) {
					end = len(d.content)
				}
				snippet = strings.TrimSpace(d.content[start:end])
				if start > 0 {
					snippet = "…" + snippet
				}
				if end < len(d.content) {
					snippet += "…"
				}
			}
			hits = append(hits, application.DocHit{
				DocMeta: application.DocMeta{ID: d.id, Title: d.title, Path: d.path},
				Snippet: snippet,
			})
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

func (s *Source) Read(id string) (application.DocFull, error) {
	for _, d := range s.docs {
		if d.id == id {
			return application.DocFull{
				DocMeta: application.DocMeta{ID: d.id, Title: d.title, Path: d.path},
				Content: d.content,
			}, nil
		}
	}
	return application.DocFull{}, os.ErrNotExist
}

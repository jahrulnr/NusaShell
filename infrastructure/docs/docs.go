// Package docs provides the product documentation corpus the agent can read
// through the docs tool (op=list / op=search / op=read). The corpus is embedded at
// build time via the resources package; a user-supplied directory may
// extend it.
package docs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"nusashell/application"
	"nusashell/infrastructure/jsonstore"
	"nusashell/resources"
)

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
	entries, err := resources.DocsFS.ReadDir("agent/docs")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := resources.DocsFS.ReadFile("agent/docs/" + e.Name())
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

// searchMatch returns the best snippet anchor for query in content. Exact
// phrases win; otherwise the earliest query term is used so multi-word
// searches still produce useful context when the terms are separated.
func searchMatch(content, query string) (index, length int) {
	if query == "" {
		return 0, 0
	}
	lower := strings.ToLower(content)
	if idx := strings.Index(lower, query); idx >= 0 {
		return idx, len(query)
	}
	terms := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	best := -1
	bestLength := 0
	for _, term := range terms {
		if idx := strings.Index(lower, term); idx >= 0 && (best < 0 || idx < best) {
			best = idx
			bestLength = len(term)
		}
	}
	return best, bestLength
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
	// Build the candidate set from the entire corpus. BM25 provides lexical
	// recall for multi-word queries whose terms are not adjacent; exact phrase
	// matching is reserved for the snippet anchor below.
	type candidate struct {
		doc      docEntry
		idx      int
		matchLen int
	}
	cands := make([]candidate, 0, len(s.docs))
	for _, d := range s.docs {
		idx, matchLen := searchMatch(d.content, q)
		cands = append(cands, candidate{doc: d, idx: idx, matchLen: matchLen})
	}

	// Ranking: BM25 over the complete corpus. Only documents containing at
	// least one query term are returned; the score determines their order.
	rank := make(map[string]int, len(cands))
	if len(cands) > 0 && q != "" {
		docs := make([]jsonstore.BM25Doc, len(cands))
		for i, c := range cands {
			docs[i] = jsonstore.BM25Doc{ID: c.doc.id, Text: c.doc.content}
		}
		results := jsonstore.NewBM25(docs).Search(q, len(cands))
		for i, r := range results {
			rank[r.ID] = i
		}
		// Preserve exact punctuation-only searches, for which BM25 has no
		// tokens to index, without making them part of normal ranking.
		if len(results) == 0 {
			for _, c := range cands {
				if c.idx >= 0 {
					rank[c.doc.id] = len(rank)
				}
			}
		}
		sort.SliceStable(cands, func(i, j int) bool {
			ri, oki := rank[cands[i].doc.id]
			rj, okj := rank[cands[j].doc.id]
			if oki != okj {
				return oki
			}
			if oki {
				return ri < rj
			}
			return cands[i].doc.id < cands[j].doc.id
		})
	}
	hits := make([]application.DocHit, 0, len(cands))
	for _, c := range cands {
		if q != "" {
			if _, ok := rank[c.doc.id]; !ok {
				continue
			}
		}
		d := c.doc
		snippet := ""
		if c.idx >= 0 {
			start := c.idx - 80
			if start < 0 {
				start = 0
			}
			end := c.idx + c.matchLen + 120
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
	return hits
}

func (s *Source) Read(id string) (application.DocFull, error) {
	id = strings.TrimSuffix(strings.TrimSpace(id), ".md")
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

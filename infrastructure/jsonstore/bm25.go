package jsonstore

import (
	"math"
	"sort"
	"strings"
)

// BM25Doc is a searchable document with an ID and text content.
type BM25Doc struct {
	ID   string
	Text string
}

// BM25Result is a ranked search result from BM25.
type BM25Result struct {
	ID    string
	Score float64
}

// BM25 is a simple in-memory BM25 implementation for <10K documents.
// No FTS5 or SQLite needed — this is the fallback when no embedding
// provider is configured. Standard parameters: k1=1.2, b=0.75.
type BM25 struct {
	docs  []BM25Doc
	avgDL float64
	index map[string][]int // term -> doc indices
	k1    float64
	b     float64
}

// NewBM25 builds an in-memory BM25 index over the given documents.
func NewBM25(docs []BM25Doc) *BM25 {
	s := &BM25{
		docs:  docs,
		index: make(map[string][]int),
		k1:    1.2,
		b:     0.75,
	}
	totalLen := 0
	for i, d := range docs {
		terms := bm25Tokenize(d.Text)
		totalLen += len(terms)
		seen := make(map[string]bool)
		for _, t := range terms {
			if !seen[t] {
				s.index[t] = append(s.index[t], i)
				seen[t] = true
			}
		}
	}
	if len(docs) > 0 {
		s.avgDL = float64(totalLen) / float64(len(docs))
	}
	return s
}

// AvgDL returns the average document length across the indexed docs.
func (s *BM25) AvgDL() float64 { return s.avgDL }

// Search returns ranked results for a query, limited to topK.
func (s *BM25) Search(query string, topK int) []BM25Result {
	terms := bm25Tokenize(query)
	if len(terms) == 0 || len(s.docs) == 0 {
		return nil
	}
	scores := make(map[int]float64)
	for _, term := range terms {
		docIDs := s.index[term]
		if len(docIDs) == 0 {
			continue
		}
		df := float64(len(docIDs))
		idf := float64(len(s.docs)) - df + 0.5
		idf /= df + 0.5
		idf = math.Log(1 + idf) // BM25+ variant
		for _, docIdx := range docIDs {
			tf := float64(bm25TermFrequency(s.docs[docIdx].Text, term))
			dl := float64(len(bm25Tokenize(s.docs[docIdx].Text)))
			score := idf * (tf * (s.k1 + 1)) / (tf + s.k1*(1-s.b+s.b*dl/s.avgDL))
			scores[docIdx] += score
		}
	}
	var results []BM25Result
	for docIdx, score := range scores {
		results = append(results, BM25Result{ID: s.docs[docIdx].ID, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

func bm25Tokenize(s string) []string {
	return strings.Fields(strings.ToLower(s))
}

func bm25TermFrequency(text, term string) int {
	count := 0
	for _, t := range bm25Tokenize(text) {
		if t == term {
			count++
		}
	}
	return count
}

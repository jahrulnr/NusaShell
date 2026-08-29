package textsim

import (
	"math"
	"testing"
)

func TestTokenizeForOverlap(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		minLen int
		want   map[string]bool
	}{
		{
			name:   "basic tokenization",
			text:   "Docker container build",
			minLen: 3,
			want:   map[string]bool{"docker": true, "container": true, "build": true},
		},
		{
			name:   "minLen filter drops short tokens",
			text:   "use go now",
			minLen: 3,
			want:   map[string]bool{"use": true, "now": true}, // "go" is 2 chars
		},
		{
			name:   "punctuation trimmed and lowercased",
			text:   "Docker, container. Build!",
			minLen: 3,
			want:   map[string]bool{"docker": true, "container": true, "build": true},
		},
		{
			name:   "empty input",
			text:   "",
			minLen: 3,
			want:   map[string]bool{},
		},
		{
			name:   "only punctuation",
			text:   ".,;:!?\"'()[]{}",
			minLen: 3,
			want:   map[string]bool{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TokenizeForOverlap(tc.text, tc.minLen)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tokens %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for tok := range tc.want {
				if !got[tok] {
					t.Errorf("missing token %q in %v", tok, got)
				}
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]bool
		want float32
	}{
		{
			name: "identical sets",
			a:    map[string]bool{"docker": true, "container": true},
			b:    map[string]bool{"docker": true, "container": true},
			want: 1.0,
		},
		{
			name: "disjoint sets",
			a:    map[string]bool{"docker": true},
			b:    map[string]bool{"git": true},
			want: 0.0,
		},
		{
			name: "empty set a",
			a:    map[string]bool{},
			b:    map[string]bool{"docker": true},
			want: 0.0,
		},
		{
			name: "empty set b",
			a:    map[string]bool{"docker": true},
			b:    map[string]bool{},
			want: 0.0,
		},
		{
			name: "partial overlap",
			a:    map[string]bool{"docker": true, "container": true, "build": true},
			b:    map[string]bool{"docker": true, "container": true, "image": true},
			want: 0.5, // intersection=2, union=4
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := JaccardSimilarity(tc.a, tc.b)
			if math.Abs(float64(got-tc.want)) > 0.001 {
				t.Errorf("JaccardSimilarity = %.4f, want %.4f", got, tc.want)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{
			name: "identical vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
		},
		{
			name: "orthogonal",
			a:    []float32{1, 0, 0},
			b:    []float32{0, 1, 0},
			want: 0.0,
		},
		{
			name: "empty",
			a:    []float32{},
			b:    []float32{1, 0},
			want: 0.0,
		},
		{
			name: "different lengths",
			a:    []float32{1, 0},
			b:    []float32{1, 0, 0},
			want: 0.0,
		},
		{
			name: "zero vector",
			a:    []float32{0, 0, 0},
			b:    []float32{1, 0, 0},
			want: 0.0,
		},
		{
			name: "3-4-5 triangle magnitude",
			a:    []float32{3, 4},
			b:    []float32{3, 4},
			want: 1.0, // |a|=|b|=5, dot=25, cos=25/25=1
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CosineSimilarity(tc.a, tc.b)
			if math.Abs(float64(got-tc.want)) > 0.001 {
				t.Errorf("CosineSimilarity = %.4f, want %.4f", got, tc.want)
			}
		})
	}
}

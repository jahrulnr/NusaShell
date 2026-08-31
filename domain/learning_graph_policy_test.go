package domain

import "testing"

func TestConsolidationPolicyConstants(t *testing.T) {
	if DefaultConsolidationSimilarityThreshold != 0.92 {
		t.Errorf("DefaultConsolidationSimilarityThreshold = %v, want 0.92", DefaultConsolidationSimilarityThreshold)
	}
	if DefaultConsolidationMinContentLen != 20 {
		t.Errorf("DefaultConsolidationMinContentLen = %d, want 20", DefaultConsolidationMinContentLen)
	}
}

func TestEdgeBuilderPolicyConstants(t *testing.T) {
	if DefaultEdgeEmbeddingThreshold != 0.85 {
		t.Errorf("DefaultEdgeEmbeddingThreshold = %v, want 0.85", DefaultEdgeEmbeddingThreshold)
	}
	if DefaultEdgeTokenOverlapThreshold != 0.3 {
		t.Errorf("DefaultEdgeTokenOverlapThreshold = %v, want 0.3", DefaultEdgeTokenOverlapThreshold)
	}
	if DefaultEdgeMinTokenLen != 3 {
		t.Errorf("DefaultEdgeMinTokenLen = %d, want 3", DefaultEdgeMinTokenLen)
	}
	if MaxSpecificTagFrequency != 12 {
		t.Errorf("MaxSpecificTagFrequency = %d, want 12", MaxSpecificTagFrequency)
	}
}

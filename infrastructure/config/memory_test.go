package config

import "testing"

func TestMemoryConsolidation(t *testing.T) {
	c := MemoryConfig{}
	c.Normalize()
	if c.Consolidation != "suggest" || c.validate() != nil {
		t.Fatalf("default = %+v", c)
	}
	c.Consolidation = "off"
	if c.validate() != nil {
		t.Fatal("off refused")
	}
	c.Consolidation = "auto"
	if c.validate() == nil {
		t.Fatal("auto accepted: it is not offered")
	}
}

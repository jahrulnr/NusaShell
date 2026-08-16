package application

import (
	"testing"

	"nusashell/domain"
)

func TestAutoUpdateDefaultOff(t *testing.T) {
	var p domain.Plugin
	if p.Manifest.AutoUpdate {
		t.Fatal("AutoUpdate must default to false (OFF)")
	}
}

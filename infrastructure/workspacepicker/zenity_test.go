package workspacepicker

import (
	"context"
	"errors"
	"testing"

	"github.com/ncruces/zenity"
)

func TestZenityChooseReturnsSelectedDirectory(t *testing.T) {
	picker := Zenity{selectDirectory: func(context.Context) (string, error) {
		return "/workspace/nusashell", nil
	}}

	got, err := picker.Choose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspace/nusashell" {
		t.Fatalf("Choose() = %q, want selected directory", got)
	}
}

func TestZenityChooseMapsDialogCancellationToContextCancellation(t *testing.T) {
	picker := Zenity{selectDirectory: func(context.Context) (string, error) {
		return "", zenity.ErrCanceled
	}}

	_, err := picker.Choose(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Choose() error = %v, want context.Canceled", err)
	}
}

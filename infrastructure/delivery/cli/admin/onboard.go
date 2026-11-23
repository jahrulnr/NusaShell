package admin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	appprovider "github.com/jahrulnr/nusashell/application/provider"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"golang.org/x/term"
)

// Onboard is the `nusashell onboard` handler — the goclaw-style guided setup:
// a banner, numbered steps, prompts with defaults, and a summary. It
// produces exactly what `provider add` would — the wizard is a friendlier
// front door, not a second provisioning path. Requires a TTY; scripted
// setup uses the provider/config flags.
func Onboard(stdin *os.File, stdout, stderr io.Writer) int {
	if !term.IsTerminal(int(stdin.Fd())) {
		fmt.Fprintln(stderr, "nusashell: onboard needs an interactive terminal; use `nusashell provider add` flags instead")
		return 2
	}
	b, err := LoadBoot(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer func() { _ = b.Close() }()
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	in := bufio.NewReader(stdin)

	fmt.Fprintln(out, "╔══════════════════════════════════════════╗")
	fmt.Fprintln(out, "║       "+ProductName+" — provider setup        ║")
	fmt.Fprintln(out, "╚══════════════════════════════════════════╝")
	fmt.Fprintln(out)
	if existing, err := b.providers.List(context.Background()); err == nil && len(existing) > 0 {
		fmt.Fprintf(out, "  %d provider(s) already configured; adding another.\n\n", len(existing))
	}

	fmt.Fprintln(out, "── Provider ──")
	id, err := promptField(in, out, "  id        ", "openai")
	if err != nil {
		return promptAborted(out, err)
	}
	fmt.Fprintln(out, "  style     1) completion  2) responses  3) messages  4) generatecontent")
	stylePick, err := promptField(in, out, "  style     ", "1")
	if err != nil {
		return promptAborted(out, err)
	}
	style := enum.StyleCompletion
	switch strings.TrimSpace(stylePick) {
	case "1", "completion":
	case "2", "responses":
		style = enum.StyleResponses
	case "3", "messages":
		style = enum.StyleMessages
	case "4", "generatecontent":
		style = enum.StyleGenerateContent
	default:
		fmt.Fprintf(out, "  unknown style %q\n", stylePick)
		return 2
	}
	endpoint, err := promptField(in, out, "  endpoint  ", defaultEndpoint(style))
	if err != nil {
		return promptAborted(out, err)
	}
	modelsRaw, err := promptField(in, out, "  models    (comma-separated, empty to import the provider catalog)", "")
	if err != nil {
		return promptAborted(out, err)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "── Credential ──")
	fmt.Fprintln(out, "  Leave empty to skip; input is hidden and stored in the SecretStore,")
	fmt.Fprintln(out, "  never in the config file. Or set env:VAR later via `provider set --key-env`.")
	key, err := promptSecret(stdin, out, "  API key   ", in)
	if err != nil {
		return promptAborted(out, err)
	}

	in2 := appprovider.ProviderInput{ID: id, Name: id, Style: style, Endpoint: endpoint}
	if key != "" {
		in2.ApiKey = &key
	}
	for m := range strings.SplitSeq(modelsRaw, ",") {
		if m = strings.TrimSpace(m); m != "" {
			in2.Models = append(in2.Models, appprovider.ModelInput{ID: m})
		}
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, "  Writing provider... ")
	provOut, err := b.providers.Upsert(context.Background(), in2)
	if err != nil {
		fmt.Fprintln(out, "FAILED")
		fmt.Fprintln(stderr, "nusashell:", err)
		return 1
	}
	fmt.Fprintln(out, "OK")
	if provOut.CatalogError != "" {
		// The record saved — a catalog failure is a warning, never a
		// failed onboard.
		fmt.Fprintf(stderr, "nusashell: warning: model catalog import failed — %s\n", provOut.CatalogError)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "╔══════════════════════════════════════════╗")
	fmt.Fprintln(out, "║           Setup complete                 ║")
	fmt.Fprintln(out, "╚══════════════════════════════════════════╝")
	fmt.Fprintln(out)
	keyNote := ""
	if key != "" {
		keyNote = " · key vaulted"
	}
	// The summary counts the stored catalog after the save-time merge —
	// imported provider ids included — not just the ids the operator
	// typed.
	modelCount := len(in2.Models) + len(provOut.Added)
	if info, err := b.providers.Get(context.Background(), id); err == nil {
		modelCount = len(info.Models)
	}
	fmt.Fprintf(out, "  provider  %s (%s) · %d model(s)%s\n", id, style, modelCount, keyNote)
	if len(provOut.Added) > 0 {
		fmt.Fprintf(out, "           imported %d model(s) from the provider catalog\n", len(provOut.Added))
	}
	fmt.Fprintln(out, "  next:    "+CLIName+"           — open the WebUI")
	return 0
}

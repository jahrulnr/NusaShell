package admin

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	appprovider "github.com/jahrulnr/nusashell/application/provider"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"golang.org/x/term"
)

// Provider is the `nusashell provider <sub>` handler — list/show/add/set/delete
// over the provider application. Secret material always goes through the
// SecretStore; records keep references only.
func Provider(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, providerUsage)
		return 0
	}
	b, err := LoadBoot(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer func() { _ = b.Close() }()
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprint(stderr, providerUsage)
			return 2
		}
		return providerList(b, stdout, stderr)
	case "show":
		if len(args) != 2 {
			fmt.Fprint(stderr, providerUsage)
			return 2
		}
		return providerShow(b, args[1], stdout, stderr)
	case "add":
		return providerWrite(b, false, args[1:], stdout, stderr, stdin)
	case "set":
		return providerWrite(b, true, args[1:], stdout, stderr, stdin)
	case "delete":
		if len(args) != 2 {
			fmt.Fprint(stderr, providerUsage)
			return 2
		}
		id := args[1]
		if err := b.providers.Delete(context.Background(), id); err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "deleted provider %s\n", id)
		// Deleting also removes the canonical vault key — warn when a
		// surviving provider references it, so the break is not silent.
		if refs, err := b.providers.List(context.Background()); err == nil {
			canonical := valueobject.SecretRef(drepo.ProviderSecretKey(id))
			for _, g := range refs {
				if g.KeyRef == canonical {
					fmt.Fprintf(stderr, "nusashell: warning: provider %s references %s — now unresolvable\n",
						g.ID, canonical)
				}
			}
		}
	default:
		fmt.Fprintf(stderr, "nusashell: unknown provider subcommand %q\n", args[0])
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	return 0
}

const providerUsage = `Usage: nusashell provider <subcommand> [flags]

  list                          list providers — sanitized, never key material
  show <id>                     provider detail and its model catalog
  add <id> --style <s> [flags]  register a provider providerID
  set <id> [flags]              edit fields; key flags rotate the credential
  delete <id>                   remove the provider and its vaulted key

add/set flags:
  --style <s>         completion | responses | messages | generatecontent
  --endpoint <url>    provider base URL
  --name <name>       display name (default: id)
  --models a,b,c      model ids to store; provider ids merge on save (providerID = model id)
  --model <m>         same as --models with one entry
  --stream            enable streaming
  --no-stream         disable streaming
  --key-env <VAR>     store an env:VAR reference — resolved at send time
  --key <literal>     vault the literal into the SecretStore (leaks to ps/history)
  --key-stdin         read the key from one stdin line (no argv, no history)
  --clear-key         drop the credential entirely (set only)
`

// providerWrite implements add and set — same flag surface; set
// read-modify-writes the existing record through InputFromInfo so unset
// flags keep their stored values. add refuses an existing healthy id —
// a silent full replace would erase models and stream settings the
// operator never typed — but permits it on a broken record, where add is
// the repair path.
func providerWrite(b *Boot, edit bool, args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	// The id is positional and precedes the flags — flag.Parse stops at
	// the first non-flag, so split it off before parsing.
	if len(args) == 0 {
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	id := args[0]
	// Path safety is the domain's identity rule; the transport adds two
	// argv-boundary constraints on top: a leading "-" means a flag landed
	// in the positional id slot, and whitespace would leave the provider
	// unaddressable — argv splits on spaces.
	if strings.HasPrefix(id, "-") || strings.ContainsAny(id, " \t\n") {
		fmt.Fprintf(stderr, "nusashell: invalid provider id %q — no flags or whitespace\n", id)
		return 2
	}
	if err := valueobject.ValidID(id); err != nil {
		fmt.Fprintf(stderr, "nusashell: invalid provider id %q — %s\n", id, err)
		return 2
	}
	fs := flag.NewFlagSet("provider", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, providerUsage) }
	style := fs.String("style", "", "completion | responses | messages | generatecontent")
	endpoint := fs.String("endpoint", "", "provider base URL")
	name := fs.String("name", "", "display name (default: id)")
	models := fs.String("models", "", "comma-separated model ids")
	fs.StringVar(models, "model", "", "alias for --models")
	streamOn := fs.Bool("stream", false, "enable streaming")
	noStream := fs.Bool("no-stream", false, "disable streaming")
	keyEnv := fs.String("key-env", "", "store an env:VAR key reference")
	keyLiteral := fs.String("key", "", "vault a literal key (leaks to ps/history)")
	keyStdin := fs.Bool("key-stdin", false, "read the key from stdin")
	clearKey := fs.Bool("clear-key", false, "drop the credential (set only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	// Contradictory key flags are a usage error, not silent last-wins.
	var keyFlags []string
	modelsSet := false
	modelsFlagSet := false
	modelFlagSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "key-env", "key", "key-stdin", "clear-key":
			keyFlags = append(keyFlags, "--"+f.Name)
		case "models":
			modelsSet = true
			modelsFlagSet = true
		case "model":
			modelsSet = true
			modelFlagSet = true
		}
	})
	if modelsFlagSet && modelFlagSet {
		fmt.Fprintln(stderr, "nusashell: --models and --model conflict — pass exactly one")
		return 2
	}
	if len(keyFlags) > 1 {
		fmt.Fprintf(stderr, "nusashell: %s conflict — pass exactly one key flag\n",
			strings.Join(keyFlags, " and "))
		return 2
	}
	if *streamOn && *noStream {
		fmt.Fprintln(stderr, "nusashell: --stream and --no-stream conflict — pick one")
		return 2
	}
	if !edit && *clearKey {
		fmt.Fprintln(stderr, "nusashell: --clear-key is a `provider set` flag — a new provider has no key to clear")
		return 2
	}

	in := appprovider.ProviderInput{ID: id}
	if edit {
		info, err := b.providers.Get(context.Background(), id)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if info.Broken {
			fmt.Fprintf(stderr, "nusashell: provider %s is corrupt — repair it with a full `provider add %s ...` or delete it\n", id, id)
			return 1
		}
		in = appprovider.InputFromInfo(info)
	} else if existing, err := b.providers.Get(context.Background(), id); err == nil && !existing.Broken {
		fmt.Fprintf(stderr, "nusashell: provider %s already exists — edit it with `provider set %s [flags]`\n", id, id)
		return 1
	}
	if *style != "" {
		s, ok := enum.ParseStyle(*style)
		if !ok {
			fmt.Fprintf(stderr, "nusashell: unknown style %q — completion | responses | messages | generatecontent\n", *style)
			return 2
		}
		in.Style = s
	}
	if *endpoint != "" {
		in.Endpoint = *endpoint
	}
	if *name != "" {
		in.Name = *name
	}
	if in.Name == "" {
		in.Name = id
	}
	if modelsSet {
		in.Models = nil
		for mid := range strings.SplitSeq(*models, ",") {
			mid = strings.TrimSpace(mid)
			if mid == "" {
				continue
			}
			in.Models = append(in.Models, appprovider.ModelInput{ID: mid})
		}
	}
	if *streamOn {
		stream := true
		in.Stream = &stream
	}
	if *noStream {
		stream := false
		in.Stream = &stream
	}
	// Required-field validation precedes any credential read — a failure
	// must not have already consumed stdin.
	if !edit && in.Style == "" {
		if !isTTY(stdin) {
			fmt.Fprintln(stderr, "nusashell: --style is required — completion | responses | messages | generatecontent (or run: nusashell onboard)")
			return 2
		}
	}
	if !edit && in.Endpoint == "" && !isTTY(stdin) {
		fmt.Fprintln(stderr, "nusashell: --endpoint is required — e.g. https://api.openai.com/v1 (or run: nusashell onboard)")
		return 2
	}
	if *clearKey {
		empty := ""
		in.ApiKey = &empty
	}
	if *keyEnv != "" {
		if strings.HasPrefix(*keyEnv, valueobject.KeyRefEnv) ||
			strings.HasPrefix(*keyEnv, valueobject.KeyRefSecret) {
			fmt.Fprintf(stderr, "nusashell: --key-env wants a bare variable name, not %q\n", *keyEnv)
			return 2
		}
		ref := valueobject.KeyRefEnv + *keyEnv
		in.ApiKey = &ref
	}
	if *keyLiteral != "" {
		fmt.Fprintln(stderr, "nusashell: note: a literal --key is visible in shell history and ps; prefer --key-stdin or --key-env")
		in.ApiKey = keyLiteral
	}
	if *keyStdin {
		k, err := readSecretLine(stdin, stderr, "API key: ")
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if k == "" {
			fmt.Fprintln(stderr, "nusashell: empty key on stdin")
			return 1
		}
		in.ApiKey = &k
	}
	// On a TTY, a bare `add <id>` drops into the same prompts onboard
	// uses — flags are the non-interactive path, not a wall.
	if f, tty := stdin.(*os.File); !edit && tty && term.IsTerminal(int(f.Fd())) {
		prompt := bufio.NewReader(f)
		if in.Style == "" {
			fmt.Fprintln(stderr, "  style  1) completion  2) responses  3) messages  4) generatecontent")
			pick, err := promptField(prompt, bufio.NewWriter(stderr), "  style", "1")
			if err != nil {
				fmt.Fprintln(stderr, "cancelled.")
				return 1
			}
			switch strings.TrimSpace(pick) {
			case "1", "completion":
				in.Style = enum.StyleCompletion
			case "2", "responses":
				in.Style = enum.StyleResponses
			case "3", "messages":
				in.Style = enum.StyleMessages
			case "4", "generatecontent":
				in.Style = enum.StyleGenerateContent
			default:
				fmt.Fprintf(stderr, "nusashell: unknown style %q\n", pick)
				return 2
			}
		}
		if in.Endpoint == "" {
			v, err := promptField(prompt, bufio.NewWriter(stderr), "  endpoint", defaultEndpoint(in.Style))
			if err != nil {
				fmt.Fprintln(stderr, "cancelled.")
				return 1
			}
			in.Endpoint = v
		}
		if !modelsSet {
			v, err := promptField(prompt, bufio.NewWriter(stderr), "  models (comma-separated, empty to import the provider catalog)", "")
			if err != nil {
				fmt.Fprintln(stderr, "cancelled.")
				return 1
			}
			for mid := range strings.SplitSeq(v, ",") {
				if mid = strings.TrimSpace(mid); mid != "" {
					in.Models = append(in.Models, appprovider.ModelInput{ID: mid})
				}
			}
		}
		// Interactive add with no key flag asks once, hidden — the key
		// never lands in argv, history, or scrollback. Bytes the field
		// reader already buffered (a multi-line paste) are the key line.
		if in.ApiKey == nil && !*clearKey {
			if k, err := readSecretLine(stdin, stderr, "API key (input hidden, empty to skip): ", prompt); err == nil && k != "" {
				in.ApiKey = &k
			}
		}
	}
	if in.Endpoint == "" {
		fmt.Fprintln(stderr, "nusashell: --endpoint is required — e.g. https://api.openai.com/v1 (or run: nusashell onboard)")
		return 2
	}
	out, err := b.providers.Upsert(context.Background(), in)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	verb := "added"
	if edit {
		verb = "updated"
	}
	fmt.Fprintf(stdout, "%s provider %s\n", verb, id)
	if len(out.Added) > 0 {
		fmt.Fprintf(stdout, "imported %d model(s) from the provider catalog\n", len(out.Added))
	}
	if out.CatalogError != "" {
		// The record saved — the import failure is a warning, not a
		// failure of the command itself.
		fmt.Fprintf(stderr, "nusashell: warning: model catalog import failed — %s\n", out.CatalogError)
	}
	return 0
}

func providerList(b *Boot, stdout, stderr io.Writer) int {
	list, err := b.providers.List(context.Background())
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "no providers configured — see nusashell provider add --help")
		return 0
	}
	for _, g := range list {
		printProviderRow(stdout, g)
	}
	return 0
}

func providerShow(b *Boot, id string, stdout, stderr io.Writer) int {
	g, err := b.providers.Get(context.Background(), id)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	printProviderRow(stdout, g)
	fmt.Fprintf(stdout, "  endpoint  %s\n  stream    %v\n", g.Endpoint, g.Stream)
	if len(g.Models) == 0 {
		fmt.Fprintln(stdout, "  models    (none)")
		return 0
	}
	fmt.Fprintln(stdout, "  models:")
	for _, m := range g.Models {
		printModelRow(stdout, m)
	}
	return 0
}

func printProviderRow(w io.Writer, g appprovider.ProviderInfo) {
	key := "no-key"
	if g.KeySet {
		key = "key-set"
	}
	if g.KeyRef != "" {
		key = g.KeyRef
	}
	broken := ""
	if g.Broken {
		broken = " broken"
	}
	fmt.Fprintf(w, "%s  %-10s style=%-10s models=%d key=%s%s\n",
		g.ID, g.Name, g.Style, len(g.Models), key, broken)
}

func printModelRow(w io.Writer, m entity.Model) {
	var extra []string
	if m.ContextWindow() > 0 {
		extra = append(extra, fmt.Sprintf("ctx=%d", m.ContextWindow()))
	}
	if m.OutputLimit() > 0 {
		extra = append(extra, fmt.Sprintf("out=%d", m.OutputLimit()))
	}
	if m.MaxImages() > 0 {
		extra = append(extra, fmt.Sprintf("img=%d", m.MaxImages()))
	}
	if t := m.ModelType(); t != "" {
		extra = append(extra, "type="+t.String())
	}
	if s := m.Style(); s != "" {
		extra = append(extra, "style="+s.String())
	}
	if mods := m.InputModalities(); len(mods) > 0 {
		var s []string
		for _, mod := range mods {
			s = append(s, mod.String())
		}
		extra = append(extra, "in="+strings.Join(s, ","))
	}
	if effs := m.SupportedEfforts(); len(effs) > 0 {
		var s []string
		for _, e := range effs {
			s = append(s, e.String())
		}
		extra = append(extra, "efforts="+strings.Join(s, ","))
	}
	fmt.Fprintf(w, "    %s", m.ID())
	if len(extra) > 0 {
		fmt.Fprintf(w, "  %s", strings.Join(extra, " "))
	}
	fmt.Fprintln(w)
}

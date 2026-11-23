package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	cliterm "github.com/jahrulnr/nusashell/infrastructure/delivery/cli/term"
)

const automationUsage = `usage: ` + CLIName + ` automation <command> [flags]

Scheduled prompts, through the running server: it owns the clock, so
changes apply at once. Every command takes --addr HOST:PORT (default
$` + EnvAddr + `, else the configured port) and --json for the raw
response.

  list                  every automation: id, name, enabled, schedule, next run
  get <id>              one automation (JSON)
  add [flags]           create one — needs --name, --prompt, a runtime, a schedule
  set <id> [flags]      edit one; only the given flags change
  pause <id>            hold its schedule
  resume <id>           plan from now (firings missed while paused stay missed)
  remove <id>           delete it and its run history
  history <id>          its recent runs, newest first
  run <id>              fire one run now; the schedule does not move

add/set flags:
  --name N         display name
  --prompt P       the prompt each run sends
  --agent A        run on this agent — or:
  --provider P     run on this provider, with
  --model M        this model
  --cron EXPR      a five-field cron expression — or:
  --at TIME        run once at TIME
  --tz ZONE        IANA zone of the schedule and of local times below
                   (default: the server's zone, or the automation's own on set)
  --thread M       new | same — a fresh thread per run, or one kept thread
  --project P      project id ("" on set leaves the project)
  --expires TIME   the automation stops after TIME ("" on set clears it)
  --notify-channel C  send each run's final reply to a chat on channel C
                      ("" on set removes the result chat)
  --notify-target T   the chat id on that channel
  --enabled=B      false pauses; an absent one keeps the current state

TIME is RFC 3339 (2026-12-01T09:30:00+07:00) or YYYY-MM-DDTHH:MM read in
the zone above.
`

// Automation is the `nusashell automation` handler.
func Automation(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, automationUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, automationUsage)
		return 0
	}
	op, rest := args[0], args[1:]
	var id string
	switch op {
	case "list", "add":
	case "get", "set", "pause", "resume", "remove", "history", "run":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			fmt.Fprintf(stderr, "%s: automation %s needs an id\n", CLIName, op)
			fmt.Fprint(stderr, automationUsage)
			return 2
		}
		id, rest = rest[0], rest[1:]
	default:
		fmt.Fprintf(stderr, "%s: unknown automation command %q\n", CLIName, op)
		fmt.Fprint(stderr, automationUsage)
		return 2
	}
	fs := flag.NewFlagSet("automation "+op, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, automationUsage) }
	addr := fs.String("addr", "", "the server's HOST:PORT")
	rawOut := fs.Bool("json", false, "print the raw response")
	var (
		name, prompt, agentID, providerID, modelID string
		cron, at, tz, thread, project, expires     string
		notifyChannel, notifyTarget                string
		enabled                                    bool
	)
	if op == "add" || op == "set" {
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&prompt, "prompt", "", "")
		fs.StringVar(&agentID, "agent", "", "")
		fs.StringVar(&providerID, "provider", "", "")
		fs.StringVar(&modelID, "model", "", "")
		fs.StringVar(&cron, "cron", "", "")
		fs.StringVar(&at, "at", "", "")
		fs.StringVar(&tz, "tz", "", "")
		fs.StringVar(&thread, "thread", "", "")
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&expires, "expires", "", "")
		fs.StringVar(&notifyChannel, "notify-channel", "", "")
		fs.StringVar(&notifyTarget, "notify-target", "", "")
		fs.BoolVar(&enabled, "enabled", false, "")
	}
	pos, err := parseInterleaved(fs, rest)
	if err != nil {
		return 2
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "%s: unexpected arguments: %s\n", CLIName, strings.Join(pos, " "))
		return 2
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	// Usage mistakes fail before a server is dialed.
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "%s: %s\n", CLIName, fmt.Sprintf(format, a...))
		return 2
	}
	if op == "add" || op == "set" {
		for _, f := range []struct {
			flag, value string
		}{{"agent", agentID}, {"provider", providerID}, {"model", modelID}} {
			if given[f.flag] && f.value == "" {
				return fail("--%s needs a value", f.flag)
			}
		}
		switch {
		case given["cron"] && given["at"]:
			return fail("one schedule only: --cron or --at")
		case (given["at"] && at == "") || (given["cron"] && cron == ""):
			return fail("an empty schedule flag is not a schedule")
		case given["tz"] && tz == "":
			return fail("--tz needs an IANA zone")
		case given["agent"] && (given["provider"] || given["model"]):
			return fail("an automation runs on --agent, or on --provider with --model — not both")
		case given["thread"] && thread != "new" && thread != "same":
			return fail("--thread is new or same, got %q", thread)
		}
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return fail("--tz: %v", err)
			}
		}
		if op == "add" {
			switch {
			case name == "" || prompt == "":
				return fail("add needs --name and --prompt")
			case agentID == "" && providerID == "" && modelID == "":
				return fail("add needs a runtime: --agent A, or --provider P --model M")
			case agentID == "" && providerID == "":
				return fail("--model needs --provider")
			case agentID == "" && modelID == "":
				return fail("a provider run needs --model")
			case cron == "" && at == "":
				return fail("add needs a schedule: --cron EXPR or --at TIME")
			case (notifyChannel == "") != (notifyTarget == ""):
				return fail("a result chat needs --notify-channel and --notify-target")
			}
		}
		if op == "set" {
			edits := false
			for _, f := range []string{"name", "prompt", "agent", "provider", "model", "cron", "at", "tz",
				"thread", "project", "expires", "notify-channel", "notify-target", "enabled"} {
				edits = edits || given[f]
			}
			if !edits {
				return fail("set needs at least one flag to change")
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := dialServer(ctx, *addr)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer srv.close()

	base := "/api/automations"
	var out []byte
	switch op {
	case "list":
		if err := srv.call(ctx, http.MethodGet, base, nil, nil, &out); err != nil {
			printErr(stderr, err)
			return 1
		}
		if *rawOut {
			printJSON(stdout, out, true)
			return 0
		}
		var list struct {
			Data []struct {
				ID        string     `json:"id"`
				Name      string     `json:"name"`
				Enabled   bool       `json:"enabled"`
				Schedule  string     `json:"schedule"`
				Cron      string     `json:"cron"`
				RunAt     *time.Time `json:"runAt"`
				Timezone  string     `json:"timezone"`
				NextRunAt *time.Time `json:"nextRunAt"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &list); err != nil {
			printErr(stderr, err)
			return 1
		}
		if len(list.Data) == 0 {
			fmt.Fprintln(stdout, "no automations — try: "+CLIName+" automation add --name N --prompt P --cron '0 9 * * *' --provider P --model M")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tENABLED\tSCHEDULE\tNEXT RUN")
		for _, a := range list.Data {
			sched := a.Schedule
			switch {
			case a.Schedule == "cron":
				sched += " " + a.Cron
			case a.RunAt != nil:
				sched += " " + a.RunAt.Format(time.RFC3339)
			}
			if a.Timezone != "" {
				sched += " (" + a.Timezone + ")"
			}
			next := "-"
			if a.Enabled && a.NextRunAt != nil {
				next = a.NextRunAt.Format(time.RFC3339)
			}
			fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\n", a.ID, cliterm.Sanitize(a.Name), a.Enabled, sched, next)
		}
		tw.Flush()
		return 0
	case "add", "set":
		doc := map[string]any{}
		if op == "set" {
			var current map[string]any
			if err := srv.call(ctx, http.MethodGet, base+"/"+url.PathEscape(id), nil, nil, &current); err != nil {
				printErr(stderr, err)
				return 1
			}
			// PUT is a full edit guarded by updatedAt; carry every field
			// the wire body knows so unset flags keep their value.
			for _, k := range []string{"name", "prompt", "agentId", "providerId", "modelId", "schedule",
				"runAt", "cron", "timezone", "thread", "projectId", "notify", "expiresAt", "enabled", "updatedAt"} {
				if v, ok := current[k]; ok {
					doc[k] = v
				}
			}
			if given["name"] {
				doc["name"] = name
			}
			if given["prompt"] {
				doc["prompt"] = prompt
			}
		} else {
			doc["name"], doc["prompt"], doc["timezone"] = name, prompt, tz
		}
		// Runtime: --agent replaces it wholly; --provider/--model move the
		// automation to a provider route and keep the sibling flag the
		// owner did not pass.
		switch {
		case given["agent"]:
			doc["agentId"] = agentID
			if op == "set" {
				doc["providerId"], doc["modelId"] = "", ""
			}
		case given["provider"] || given["model"]:
			if op == "set" {
				doc["agentId"] = ""
			}
			if given["provider"] {
				doc["providerId"] = providerID
			}
			if given["model"] {
				doc["modelId"] = modelID
			}
		case op == "add":
			if agentID != "" {
				doc["agentId"] = agentID
			} else {
				doc["providerId"], doc["modelId"] = providerID, modelID
			}
		}
		// Local times read in the schedule's zone: --tz when given, else
		// the automation's own (set), else the server's (add) — resolved
		// lazily so a schedule without local times costs no extra call.
		zone := tz
		var zoneErr error
		parseTime := func(v string) (string, error) {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t.Format(time.RFC3339), nil
			}
			if zone == "" && zoneErr == nil {
				if z, _ := doc["timezone"].(string); op == "set" && z != "" {
					zone = z
				} else {
					var list struct {
						Timezone string `json:"timezone"`
					}
					if err := srv.call(ctx, http.MethodGet, base, nil, nil, &list); err != nil {
						zoneErr = err
						return "", err
					}
					zone = list.Timezone
				}
			}
			if zoneErr != nil {
				return "", zoneErr
			}
			t, err := parseAutomationTime(v, zone)
			if err != nil {
				return "", err
			}
			return t.Format(time.RFC3339), nil
		}
		timeOrExit := func(v string) (string, int) {
			s, err := parseTime(v)
			if err != nil {
				printErr(stderr, err)
				if zoneErr != nil {
					return "", 1
				}
				return "", 2
			}
			return s, 0
		}
		switch {
		case given["cron"]:
			doc["schedule"], doc["cron"] = "cron", cron
		case given["at"]:
			runAt, code := timeOrExit(at)
			if code != 0 {
				return code
			}
			doc["schedule"], doc["runAt"] = "once", runAt
		}
		if given["tz"] {
			doc["timezone"] = tz
		}
		if given["thread"] {
			doc["thread"] = thread
		}
		if given["project"] {
			doc["projectId"] = project
		}
		if given["expires"] {
			if expires == "" {
				doc["expiresAt"] = nil
			} else if exp, code := timeOrExit(expires); code != 0 {
				return code
			} else {
				doc["expiresAt"] = exp
			}
		}
		if given["notify-channel"] || given["notify-target"] {
			if given["notify-channel"] && notifyChannel == "" {
				doc["notify"] = nil
			} else {
				chat, _ := doc["notify"].(map[string]any)
				if chat == nil {
					chat = map[string]any{}
				}
				if given["notify-channel"] {
					chat["channel"] = notifyChannel
				}
				if given["notify-target"] {
					chat["chatId"] = notifyTarget
				}
				ch, _ := chat["channel"].(string)
				tgt, _ := chat["chatId"].(string)
				if ch == "" || tgt == "" {
					return fail("a result chat needs --notify-channel and --notify-target")
				}
				doc["notify"] = chat
			}
		}
		if given["enabled"] {
			doc["enabled"] = enabled
		}
		body, _ := json.Marshal(doc)
		if op == "add" {
			err = srv.call(ctx, http.MethodPost, base, nil, body, &out)
		} else {
			err = srv.call(ctx, http.MethodPut, base+"/"+url.PathEscape(id), nil, body, &out)
		}
	case "get":
		err = srv.call(ctx, http.MethodGet, base+"/"+url.PathEscape(id), nil, nil, &out)
	case "pause", "resume":
		body, _ := json.Marshal(map[string]any{"enabled": op == "resume"})
		err = srv.call(ctx, http.MethodPatch, base+"/"+url.PathEscape(id), nil, body, &out)
	case "remove":
		err = srv.call(ctx, http.MethodDelete, base+"/"+url.PathEscape(id), nil, nil, nil)
	case "history":
		err = srv.call(ctx, http.MethodGet, base+"/"+url.PathEscape(id)+"/runs", nil, nil, &out)
	case "run":
		err = srv.call(ctx, http.MethodPost, base+"/"+url.PathEscape(id)+"/runs", nil, nil, &out)
	}
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	switch op {
	case "remove":
		fmt.Fprintf(stdout, "removed automation %s\n", id)
		return 0
	case "pause", "resume":
		if !*rawOut {
			verb := "paused"
			if op == "resume" {
				verb = "resumed"
			}
			fmt.Fprintf(stdout, "%s automation %s\n", verb, id)
			return 0
		}
	case "run":
		if !*rawOut {
			var r struct {
				ID, Status string
			}
			if json.Unmarshal(out, &r) == nil && r.ID != "" {
				fmt.Fprintf(stdout, "run %s %s\n", r.ID, r.Status)
				return 0
			}
		}
	case "history":
		if !*rawOut {
			var runs struct {
				Data []struct {
					ID             string     `json:"id"`
					ScheduledAt    time.Time  `json:"scheduledAt"`
					EndedAt        *time.Time `json:"endedAt"`
					ConversationID string     `json:"conversationId"`
					Status         string     `json:"status"`
					Note           string     `json:"note"`
				} `json:"data"`
			}
			if json.Unmarshal(out, &runs) == nil {
				if len(runs.Data) == 0 {
					fmt.Fprintln(stdout, "no runs yet")
					return 0
				}
				tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "RUN\tSTATUS\tSCHEDULED\tENDED\tTHREAD\tNOTE")
				for _, r := range runs.Data {
					ended := "-"
					if r.EndedAt != nil {
						ended = r.EndedAt.Format(time.RFC3339)
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.ScheduledAt.Format(time.RFC3339),
						ended, r.ConversationID, cliterm.Sanitize(r.Note))
				}
				tw.Flush()
				return 0
			}
		}
	}
	printJSON(stdout, out, *rawOut)
	return 0
}

// parseAutomationTime reads --at/--expires: RFC 3339, or YYYY-MM-DDTHH:MM
// in zone.
func parseAutomationTime(value, zone string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown time zone %q", zone)
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", value, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("bad time %q — want RFC 3339 or YYYY-MM-DDTHH:MM", value)
}

// printJSON renders a JSON response: indented, or verbatim with --json.
func printJSON(w io.Writer, raw []byte, rawOut bool) {
	if rawOut {
		fmt.Fprintln(w, strings.TrimSpace(string(raw)))
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(raw)
	}
	fmt.Fprintln(w, strings.TrimSpace(pretty.String()))
}

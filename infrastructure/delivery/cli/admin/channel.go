package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	cliterm "github.com/jahrulnr/nusashell/infrastructure/delivery/cli/term"
)

const channelUsage = `usage: ` + CLIName + ` channel <command> [flags]

Chat channels, through the running server: it holds the connections, so
changes apply at once. Every command takes --addr HOST:PORT (default
$` + EnvAddr + `, else the configured port).

  list                         each channel's state
  <channel> show               its settings (JSON)
  <channel> set [flags]        change settings; only the given flags change
  whatsapp groups              joined groups: JID, name, allowed
  whatsapp send --to T --text S
  whatsapp send --to T --file PATH [--caption S]
  whatsapp send < request.json  any message type (see below)
  telegram send --to CHAT_ID --text S [--thread TOPIC]  starts a message
  whatsapp search [flags] [text]  kept messages, newest first (JSON)
      -n N  page size 1-500 (50)   --cursor C  nextCursor of the last page
      --chat JID  --sender JID     --raw  include WhatsApp's own payload
  whatsapp contacts [-n N] [--page N] [text]  synced contacts (JSON)
  whatsapp user JID            a user's profile through the connection

WhatsApp set:
  --enabled=B  --group-policy disabled|mention|all
  --allow "n1,n2"  replace the number allowlist ("" allows no one, "*" everyone)
  --allow-add N  --allow-remove N            (repeatable)
  --groups "jid,jid"  replace the group allowlist (…@g.us; see groups)
  --group-add JID  --group-remove JID        (repeatable)
  --read-receipts=B  --send-typing=B
  --retention-days N  drop kept messages older than N days (0 keeps them)
  --send-pacing N     least seconds between two sends to one chat (0-60)
  --queue-cap N       most messages waiting for one chat (1-200)

Telegram set:
  --enabled=B  --group-policy disabled|mention|all
  --allow "id,id"  --allow-add ID  --allow-remove ID   user ids ("*" everyone)
  --groups "-id,-id"  --group-add ID  --group-remove ID  group ids
  --token-stdin       read the bot token from standard input ("" removes it)

WhatsApp send request (JSON on stdin): {"target": number or JID, "type": …}
  text {text}; reply {text, messageId, senderId, quotedText?};
  edit {messageId, text}; revoke {messageId}; reaction {messageId, senderId, text};
  image|video|audio|document {path or data (base64), mime?, name?, caption?};
  sticker {path or data: WebP, animated?}; location {latitude, longitude, name?, address?};
  contact {name, vcard}; event {name, startTime, endTime?, description?, joinLink?};
  poll {text: question, options: [2-12], maxSelectable?: 0 = any}
A path is read here and sent as data. The answer is {"messageId": …}.
`

// channelSetFields maps the shared set flags onto each channel's settings
// fields.
var channelSetFields = map[string]map[string]string{
	"whatsapp": {"allow": "allowedNumbers", "allow-add": "addNumbers", "allow-remove": "removeNumbers",
		"groups": "allowedGroups", "group-add": "addGroups", "group-remove": "removeGroups"},
	"telegram": {"allow": "allowedUserIds", "allow-add": "addUserIds", "allow-remove": "removeUserIds",
		"groups": "allowedGroupIds", "group-add": "addGroupIds", "group-remove": "removeGroupIds"},
}

// Channel is the `nusashell channel` handler.
func Channel(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, channelUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, channelUsage)
		return 0
	}
	name, op, rest := "", "list", args[1:]
	if args[0] != "list" {
		if len(args) < 2 {
			fmt.Fprint(stderr, channelUsage)
			return 2
		}
		name, op, rest = args[0], args[1], args[2:]
	}
	fs := flag.NewFlagSet("channel "+strings.TrimSpace(name+" "+op), flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, channelUsage) }
	addr := fs.String("addr", "", "the server's HOST:PORT")
	// Flags of the commands that take them; unused ones stay unset.
	var (
		enabled, readReceipts, sendTyping, raw, tokenStdin bool
		policy, allow, groups, to, text, file, caption     string
		thread                                             string
		cursor, chat, sender                               string
		allowAdd, allowRemove, groupAdd, groupRemove       repeated
		retention, pacing, queueCap, limit, page           int
	)
	switch op {
	case "set":
		fs.BoolVar(&enabled, "enabled", false, "")
		fs.StringVar(&policy, "group-policy", "", "")
		fs.StringVar(&allow, "allow", "", "")
		fs.Var(&allowAdd, "allow-add", "")
		fs.Var(&allowRemove, "allow-remove", "")
		fs.StringVar(&groups, "groups", "", "")
		fs.Var(&groupAdd, "group-add", "")
		fs.Var(&groupRemove, "group-remove", "")
		if name == "whatsapp" {
			fs.BoolVar(&readReceipts, "read-receipts", false, "")
			fs.BoolVar(&sendTyping, "send-typing", false, "")
			fs.IntVar(&retention, "retention-days", 0, "")
			fs.IntVar(&pacing, "send-pacing", 0, "")
			fs.IntVar(&queueCap, "queue-cap", 0, "")
		}
		if name == "telegram" {
			fs.BoolVar(&tokenStdin, "token-stdin", false, "")
		}
	case "send":
		fs.StringVar(&to, "to", "", "")
		fs.StringVar(&text, "text", "", "")
		fs.StringVar(&file, "file", "", "")
		fs.StringVar(&caption, "caption", "", "")
		fs.StringVar(&thread, "thread", "", "")
	case "search":
		fs.IntVar(&limit, "n", 50, "")
		fs.StringVar(&cursor, "cursor", "", "")
		fs.StringVar(&chat, "chat", "", "")
		fs.StringVar(&sender, "sender", "", "")
		fs.BoolVar(&raw, "raw", false, "")
	case "contacts":
		fs.IntVar(&limit, "n", 50, "")
		fs.IntVar(&page, "page", 1, "")
	case "list", "show", "groups", "user":
	default:
		fmt.Fprintf(stderr, "%s: unknown channel command %q\n", CLIName, op)
		fmt.Fprint(stderr, channelUsage)
		return 2
	}
	pos, err := parseInterleaved(fs, rest)
	if err != nil {
		return 2
	}
	wantPos := map[string]int{"search": 1, "contacts": 1, "user": 1}[op]
	if len(pos) > wantPos || (op == "user" && len(pos) != 1) {
		fmt.Fprintf(stderr, "%s: unexpected arguments: %s\n", CLIName, strings.Join(pos, " "))
		return 2
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	// Build the request before calling the server, so a usage mistake
	// fails without one.
	var body []byte
	switch op {
	case "set":
		fields, ok := channelSetFields[name]
		if !ok {
			fmt.Fprintf(stderr, "%s: no settings flags for channel %q\n", CLIName, name)
			return 2
		}
		doc := map[string]any{}
		split := func(v string) []string {
			out := []string{}
			for p := range strings.SplitSeq(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
		for _, sf := range []struct {
			flag, field string
			value       any
		}{{"enabled", "enabled", enabled}, {"group-policy", "groupPolicy", policy}, {"read-receipts", "readReceipts", readReceipts},
			{"send-typing", "sendTyping", sendTyping}, {"retention-days", "retentionDays", retention},
			{"send-pacing", "sendPacing", pacing}, {"queue-cap", "queueCap", queueCap}} {
			if given[sf.flag] {
				doc[sf.field] = sf.value
			}
		}
		for flagName, v := range map[string][]string{"allow": split(allow), "groups": split(groups),
			"allow-add": allowAdd, "allow-remove": allowRemove, "group-add": groupAdd, "group-remove": groupRemove} {
			if given[flagName] {
				doc[fields[flagName]] = v
			}
		}
		if tokenStdin {
			tok, err := readSecretLine(stdin, io.Discard, "")
			if err != nil {
				printErr(stderr, err)
				return 1
			}
			doc["token"] = tok
		}
		if len(doc) == 0 {
			fmt.Fprintf(stderr, "%s: channel %s set needs at least one flag\n", CLIName, name)
			return 2
		}
		body, _ = json.Marshal(doc)
	case "send":
		req := map[string]any{}
		switch {
		case to != "":
			req["target"] = to
			switch {
			case file != "" && text != "":
				fmt.Fprintf(stderr, "%s: send takes --text or --file, not both (--caption goes with a file)\n", CLIName)
				return 2
			case file != "":
				req["path"] = file
				if caption != "" {
					req["caption"] = caption
				}
			case text != "":
				req["type"], req["text"] = "text", text
				if thread != "" {
					// A forum message carries its topic, or it lands in the
					// forum's main stream instead of the thread it answers.
					req["thread"] = thread
				}
			default:
				fmt.Fprintf(stderr, "%s: send --to needs --text or --file\n", CLIName)
				return 2
			}
		case text != "" || file != "" || caption != "":
			fmt.Fprintf(stderr, "%s: send needs --to with --text or --file\n", CLIName)
			return 2
		default:
			raw, err := io.ReadAll(io.LimitReader(stdin, 64<<20))
			if err != nil {
				printErr(stderr, err)
				return 1
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				fmt.Fprintf(stderr, "%s: the send request on stdin is not a JSON object: %v\n", CLIName, err)
				return 2
			}
		}
		// A local file goes as data; the server may not see this path.
		if p, ok := req["path"].(string); ok {
			data, err := os.ReadFile(p)
			if err != nil {
				printErr(stderr, err)
				return 1
			}
			delete(req, "path")
			req["data"] = base64.StdEncoding.EncodeToString(data)
			if _, ok := req["name"]; !ok {
				req["name"] = filepath.Base(p)
			}
			mt, _ := req["mime"].(string)
			if mt == "" {
				if mt = mime.TypeByExtension(filepath.Ext(p)); mt == "" {
					mt = http.DetectContentType(data)
				}
				req["mime"] = mt
			}
			if _, ok := req["type"]; !ok {
				switch kind, _, _ := strings.Cut(mt, "/"); kind {
				case "image", "video", "audio":
					req["type"] = kind
				default:
					req["type"] = "document"
				}
			}
		}
		body, _ = json.Marshal(req)
	}

	timeout := 30 * time.Second
	if op == "send" {
		timeout = 3 * time.Minute // the chat's queue may hold it a while
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	srv, err := dialServer(ctx, *addr)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer srv.close()
	base := "/api/channels/" + url.PathEscape(name)
	var out []byte
	q := url.Values{}
	switch op {
	case "list":
		var list struct {
			Data []struct {
				Name, State, Detail, Account string
				Enabled                      bool
			} `json:"data"`
		}
		if err := srv.call(ctx, http.MethodGet, "/api/channels", nil, nil, &list); err != nil {
			printErr(stderr, err)
			return 1
		}
		tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "CHANNEL\tENABLED\tSTATE\tACCOUNT\tDETAIL")
		for _, c := range list.Data {
			fmt.Fprintf(tw, "%s\t%v\t%s\t%s\t%s\n", c.Name, c.Enabled, c.State, cliterm.Sanitize(c.Account), cliterm.Sanitize(c.Detail))
		}
		tw.Flush()
		return 0
	case "show":
		err = srv.call(ctx, http.MethodGet, base+"/settings", nil, nil, &out)
	case "set":
		err = srv.call(ctx, http.MethodPut, base+"/settings", nil, body, &out)
	case "groups":
		err = srv.call(ctx, http.MethodGet, base+"/groups", nil, nil, &out)
	case "send":
		err = srv.call(ctx, http.MethodPost, base+"/send", nil, body, &out)
	case "user":
		q.Set("id", pos[0])
		err = srv.call(ctx, http.MethodGet, base+"/user", q, nil, &out)
	case "contacts":
		if len(pos) == 1 {
			q.Set("q", pos[0])
		}
		q.Set("limit", strconv.Itoa(limit))
		q.Set("page", strconv.Itoa(page))
		err = srv.call(ctx, http.MethodGet, base+"/contacts", q, nil, &out)
	case "search":
		if len(pos) == 1 {
			q.Set("q", pos[0])
		}
		q.Set("limit", strconv.Itoa(limit))
		for k, v := range map[string]string{"cursor": cursor, "chat": chat, "sender": sender} {
			if v != "" {
				q.Set(k, v)
			}
		}
		if raw {
			q.Set("raw", "1")
		}
		err = srv.call(ctx, http.MethodGet, base+"/messages", q, nil, &out)
	}
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(out)
	}
	fmt.Fprintln(stdout, strings.TrimSpace(pretty.String()))
	return 0
}

// parseInterleaved parses flags that may come after positional arguments
// ("search hello -n 5"); everything after "--" is positional.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// flag.Parse consumed a "--" terminator when the word before the
		// rest is one: all of the rest is positional.
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

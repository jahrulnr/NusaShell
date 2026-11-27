package admin

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const modelListUsage = `usage: ` + CLIName + ` model list (--agent <id> | --provider <id>) [--addr HOST:PORT] [--json]

The catalog a runtime can use, through the running server. Exactly one of
--agent and --provider. Neither, or both, is a usage error and does not
call the server. The command does not start an agent.

--json prints one ModelCatalog object. known lists each model. A known
empty catalog prints "no models". unknown prints unknown, the default
that applies, and the reason — it does not print a list.
`

type catalogModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type catalogDefault struct {
	ID string `json:"id"`
	By string `json:"by"`
}

// catalogDoc is one ModelCatalog. Data is omitted when the catalog is
// unknown; a pointer distinguishes that from a known empty list.
type catalogDoc struct {
	Status  string          `json:"status"`
	Source  string          `json:"source"`
	Data    *[]catalogModel `json:"data,omitempty"`
	Default *catalogDefault `json:"default,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

func modelList(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, modelListUsage)
			return 0
		}
	}
	fs := flag.NewFlagSet("model list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, modelListUsage) }
	addr := fs.String("addr", "", "the server's HOST:PORT")
	agent := fs.String("agent", "", "list this agent's catalog")
	provider := fs.String("provider", "", "list this provider's catalog")
	rawOut := fs.Bool("json", false, "print one ModelCatalog object")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 0 || (*agent == "") == (*provider == "") {
		fmt.Fprint(stderr, modelListUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := dialServer(ctx, *addr)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer srv.close()

	var doc catalogDoc
	var note string
	if *agent != "" {
		var raw []byte
		if err := srv.call(ctx, http.MethodGet, "/api/agents/"+url.PathEscape(*agent)+"/models", nil, nil, &raw); err != nil {
			printErr(stderr, err)
			return 1
		}
		doc, err = decodeCatalog(raw)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
	} else {
		var raw, settings []byte
		if err := srv.call(ctx, http.MethodGet, "/api/providers/"+url.PathEscape(*provider), nil, nil, &raw); err != nil {
			printErr(stderr, err)
			return 1
		}
		if err := srv.call(ctx, http.MethodGet, "/api/settings", nil, nil, &settings); err != nil {
			printErr(stderr, err)
			return 1
		}
		doc, note, err = providerCatalog(*provider, raw, settings)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
	}
	return writeCatalog(stdout, stderr, doc, note, *rawOut)
}

// decodeCatalog reads the agent route's body. A missing status is not a
// catalog. Unknown drops any data the body carried, so a list is never
// how unknown is spelled.
func decodeCatalog(raw []byte) (catalogDoc, error) {
	var in struct {
		Status  string          `json:"status"`
		Source  string          `json:"source"`
		Data    json.RawMessage `json:"data"`
		Default *catalogDefault `json:"default"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return catalogDoc{}, err
	}
	if in.Status != "known" && in.Status != "unknown" {
		return catalogDoc{}, fmt.Errorf("catalog has no status")
	}
	if in.Status == "unknown" {
		return catalogDoc{Status: in.Status, Source: in.Source, Default: in.Default, Reason: in.Reason}, nil
	}
	if len(in.Data) == 0 || string(in.Data) == "null" {
		return catalogDoc{}, fmt.Errorf("known catalog has no data")
	}
	rows := []catalogModel{}
	if err := json.Unmarshal(in.Data, &rows); err != nil {
		return catalogDoc{}, err
	}
	return catalogDoc{Status: "known", Source: in.Source, Data: &rows}, nil
}

// providerCatalog maps a provider record onto the same object the agent
// route returns. A record that did not decode is unknown. An empty model
// array is a known empty catalog, even when the credential is broken.
func providerCatalog(id string, raw, settings []byte) (catalogDoc, string, error) {
	var rec struct {
		Endpoint string `json:"endpoint"`
		Broken   bool   `json:"broken"`
		Models   []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return catalogDoc{}, "", err
	}
	prov, model := threadDefault(settings)
	def := catalogDefault{By: "unset"}
	if prov == id && model != "" {
		def = catalogDefault{ID: model, By: "settings"}
	}
	if rec.Broken && strings.TrimSpace(rec.Endpoint) == "" {
		return catalogDoc{
			Status: "unknown", Source: "config", Default: &def,
			Reason: "provider record cannot be read",
		}, "", nil
	}
	rows := make([]catalogModel, 0, len(rec.Models))
	for _, m := range rec.Models {
		if m.ID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		rows = append(rows, catalogModel{
			ID: m.ID, Name: name, Current: def.By == "settings" && def.ID == m.ID,
		})
	}
	note := ""
	if rec.Broken {
		note = "credential: broken"
	}
	return catalogDoc{Status: "known", Source: "config", Data: &rows}, note, nil
}

func threadDefault(raw []byte) (provider, model string) {
	var doc struct {
		Models struct {
			Threads struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"threads"`
		} `json:"models"`
	}
	_ = json.Unmarshal(raw, &doc)
	return strings.TrimSpace(doc.Models.Threads.Provider), strings.TrimSpace(doc.Models.Threads.Model)
}

func writeCatalog(stdout, stderr io.Writer, doc catalogDoc, note string, jsonOut bool) int {
	if doc.Status != "known" && doc.Status != "unknown" {
		fmt.Fprintln(stderr, CLIName+": catalog has no status")
		return 1
	}
	if doc.Status == "unknown" {
		doc.Data = nil
	}
	if jsonOut {
		raw, err := json.Marshal(doc)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	if doc.Status == "unknown" {
		fmt.Fprintln(stdout, "unknown")
		fmt.Fprintln(stdout, defaultLine(doc.Default))
		if doc.Reason != "" {
			fmt.Fprintln(stdout, doc.Reason)
		}
		return 0
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	if doc.Data == nil || len(*doc.Data) == 0 {
		fmt.Fprintln(stdout, "no models")
		return 0
	}
	for _, m := range *doc.Data {
		line := m.ID + "  " + m.Name
		if m.Current {
			line += "  current"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

func defaultLine(d *catalogDefault) string {
	if d == nil || d.By == "" || d.By == "unset" {
		return "default: unset"
	}
	if d.ID == "" {
		return "default: (" + d.By + ")"
	}
	return "default: " + d.ID + " (" + d.By + ")"
}

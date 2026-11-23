package system

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func taskSpecFixture() Spec {
	return Spec{
		Name:        "nusashell",
		Description: "NusaShell",
		Exec:        `C:\Users\Ann\AppData\Local\NusaShell\bin\nusashell.exe`,
		Args:        []string{"serve"},
		WorkingDir:  `C:\Users\Ann\AppData\Local\nusashell`,
		Env:         map[string]string{"CONFIG_PATH": `C:\Users\Ann\AppData\Roaming\nusashell\config.json`},
	}
}

func TestTaskXMLRendersLogonTask(t *testing.T) {
	got, err := renderTaskXML(taskSpecFixture(), `CORP\ann`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<LogonTrigger>`,
		`<UserId>CORP\ann</UserId>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`<Command>C:\Users\Ann\AppData\Local\NusaShell\bin\nusashell.exe</Command>`,
		`<Arguments>serve</Arguments>`,
		`<WorkingDirectory>C:\Users\Ann\AppData\Local\nusashell</WorkingDirectory>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("task XML missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "<Password>") || strings.Contains(got, "LogonType>Password") {
		t.Fatalf("task XML stores a password:\n%s", got)
	}
	if strings.Contains(got, "CONFIG_PATH") {
		t.Fatalf("Spec.Env leaked into the task XML — Task Scheduler has no environment:\n%s", got)
	}
}

func TestTaskXMLEscapesValues(t *testing.T) {
	spec := taskSpecFixture()
	spec.Exec = `C:\Program Files & More\nusashell.exe`
	spec.Description = `NusaShell <server> & "agents"`
	spec.Args = []string{"serve", "--tag=a&b"}
	got, err := renderTaskXML(spec, `DOMAIN\user`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<Command>C:\Program Files &amp; More\nusashell.exe</Command>`,
		`NusaShell &lt;server&gt; &amp; &quot;agents&quot;`,
		`--tag=a&amp;b`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("task XML missing %q\n%s", want, got)
		}
	}
}

func TestTaskXMLRejectsLineBreaks(t *testing.T) {
	for _, mutate := range []func(*Spec){
		func(s *Spec) { s.Exec = "C:\\x\nrm" },
		func(s *Spec) { s.WorkingDir = "C:\\x\r\ny" },
		func(s *Spec) { s.Description = "ok\nbad" },
	} {
		spec := taskSpecFixture()
		mutate(&spec)
		if _, err := renderTaskXML(spec, `DOMAIN\user`); err == nil {
			t.Fatal("line break was not rejected")
		}
	}
}

func TestTaskXMLArgsAreQuotedForCommandLine(t *testing.T) {
	spec := taskSpecFixture()
	spec.Args = []string{"serve", "--name=with space", `say "hi"`}
	got, err := renderTaskXML(spec, `DOMAIN\user`)
	if err != nil {
		t.Fatal(err)
	}
	// CommandLineToArgvW quoting first, then XML escaping on top: the
	// \" inside "say \"hi\"" survives as a literal backslash + &quot;.
	want := `<Arguments>serve &quot;--name=with space&quot; &quot;say \&quot;hi\&quot;&quot;</Arguments>`
	if !strings.Contains(got, want) {
		t.Fatalf("task XML arguments =\n%s\nwant %q", got, want)
	}
}

func TestTaskXMLBytesAreUTF16LEWithBOM(t *testing.T) {
	raw := taskXMLBytes("<?xml version=\"1.0\" encoding=\"UTF-16\"?>\r\n<Task/>")
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xFE {
		t.Fatalf("missing UTF-16LE BOM: % x", raw[:4])
	}
	// The declaration must decode back to the same text.
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 2; i+1 < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	got := string(utf16.Decode(u16))
	if !strings.Contains(got, `encoding="UTF-16"`) || !strings.HasSuffix(got, "<Task/>") {
		t.Fatalf("round trip failed: %q", got)
	}
}

func TestWinArgQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"serve", "serve"},
		{"--port=10994", "--port=10994"},
		{"", `""`},
		{"with space", `"with space"`},
		{"a\tb", "\"a\tb\""},
		{`say "hi"`, `"say \"hi\""`},
		{`trailing\`, `trailing\`},
		{`a\"b`, `"a\\\"b"`},
	} {
		if got := winArgQuote(tc.in); got != tc.want {
			t.Fatalf("winArgQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseTaskInfo(t *testing.T) {
	info, err := parseTaskInfo("Running|0|11/24/2026 9:00:00 AM|11/25/2026 9:00:00 AM\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "Running" || info.LastResult != 0 {
		t.Fatalf("info = %+v", info)
	}
	if info.serviceState() != StateRunning {
		t.Fatalf("state = %v", info.serviceState())
	}
	if report := info.report("nusashell"); !strings.Contains(report, "Running") || !strings.Contains(report, "nusashell") {
		t.Fatalf("report = %q", report)
	}
}

func TestTaskInfoStateMapping(t *testing.T) {
	for _, tc := range []struct {
		state  string
		result int64
		want   State
	}{
		{"Running", 0, StateRunning},
		{"Ready", 0, StateStopped},
		{"Ready", taskResultNeverRan, StateStopped},
		{"Ready", taskResultTerminated, StateStopped},
		{"Disabled", 0, StateStopped},
		{"Queued", 0, StateStopped},
		{"Ready", taskResultRunning, StateRunning},
		{"Ready", 1, StateFailed},
		{"Ready", 0x80070002, StateFailed},
		{"Unknown", 0, StateUnknown},
		{"", 0, StateUnknown},
	} {
		info := taskInfo{State: tc.state, LastResult: tc.result}
		if got := info.serviceState(); got != tc.want {
			t.Fatalf("{%s %d} = %v, want %v", tc.state, tc.result, got, tc.want)
		}
	}
}

func TestParseTaskInfoRejectsGarbage(t *testing.T) {
	for _, out := range []string{"", "no pipe", "Ready|notanint"} {
		if _, err := parseTaskInfo(out); err == nil {
			t.Fatalf("parseTaskInfo(%q) succeeded", out)
		}
	}
}

func TestSchedCSVStatus(t *testing.T) {
	status, err := schedCSVStatus(`"\\nusashell","11/25/2026 9:00:00 AM","Ready"` + "\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if status != "Ready" {
		t.Fatalf("status = %q", status)
	}
	if _, err := schedCSVStatus("garbage-no-columns"); err == nil {
		t.Fatal("unparseable row accepted")
	}
}

func TestSchedCSVStateLocalizedDegrades(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   State
	}{
		{"Running", StateRunning},
		{"Ready", StateStopped},
		{"Disabled", StateStopped},
		{"Queued", StateStopped},
		{"Could not start", StateFailed},
		{"Bereit", StateUnknown}, // a German locale must not guess
		{"Prêt", StateUnknown},
		{"", StateUnknown},
	} {
		if got := schedCSVState(tc.status); got != tc.want {
			t.Fatalf("schedCSVState(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

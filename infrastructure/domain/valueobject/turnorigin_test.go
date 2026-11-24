package valueobject

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func TestTurnOriginNormalizesAndDescribes(t *testing.T) {
	cases := []struct {
		name     string
		origin   TurnOrigin
		ok       bool
		wantName string
		want     string
	}{
		{"web drops name", TurnOrigin{Kind: enum.TurnOriginWeb, Name: "ignored"}, true, "", "web"},
		{"channel names the chat", TurnOrigin{Kind: enum.TurnOriginChannel, Name: " telegram "}, true, "telegram", "chat channel telegram"},
		{"channel without a name", TurnOrigin{Kind: enum.TurnOriginChannel}, true, "", "a chat channel"},
		{"automation quotes its name", TurnOrigin{Kind: enum.TurnOriginAutomation, Name: "Daily report"}, true, "Daily report", `automation "Daily report"`},
		{"automation without a name", TurnOrigin{Kind: enum.TurnOriginAutomation}, true, "", "an automation run"},
		{"conversation drops name", TurnOrigin{Kind: enum.TurnOriginConversation, Name: "ignored"}, true, "", "another NusaShell thread"},
		{"zero value", TurnOrigin{}, false, "", ""},
		{"unknown kind", TurnOrigin{Kind: "carrier-pigeon"}, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.origin.Normalized()
			if ok != tc.ok {
				t.Fatalf("Normalized ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				if tc.origin.Description() != "" {
					t.Fatalf("Description = %q, want empty", tc.origin.Description())
				}
				return
			}
			if got.Name != tc.wantName {
				t.Fatalf("normalized name = %q, want %q", got.Name, tc.wantName)
			}
			if desc := got.Description(); desc != tc.want {
				t.Fatalf("Description = %q, want %q", desc, tc.want)
			}
		})
	}
}

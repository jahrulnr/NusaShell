package memory

import "testing"

func TestHasRecallIntent(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		// English recall phrases
		{"en: recall", "can you recall what we did", true},
		{"en: remember", "do you remember the gateway config", true},
		{"en: last time", "last time we discussed the API design", true},
		{"en: we discussed", "we discussed this before", true},
		{"en: previously", "previously you mentioned a bug", true},
		{"en: earlier", "earlier in this conversation", true},
		{"en: before", "you said before that the queue was broken", true},
		{"en: yesterday", "yesterday we fixed the memory leak", true},
		{"en: last week", "last week we worked on the parser", true},
		{"en: as mentioned", "as mentioned earlier", true},
		{"en: as we discussed", "as we discussed yesterday", true},
		{"en: refers to", "this refers to our previous work", true},

		// Indonesian recall phrases
		{"id: sebelumnya", "sebelumnya kita bahas soal gateway", true},
		{"id: kemarin", "kemarin kita perbaiki bugnya", true},
		{"id: ingat", "kamu ingat kan servernya", true},
		{"id: ingat ga", "ingat ga kita pernah bahas ini", true},
		{"id: bahas", "kita pernah bahas ini kan", true},
		{"id: itu lalu", "yang itu lalu kita kerjakan", true},
		{"id: tadi", "tadi kita udah selesaiin", true},
		{"id: sebelum", "sebelum itu kita bikin dulu", true},
		{"id: kemaren", "kemaren udah aku coba", true},
		{"id: waktu itu", "waktu itu kita pakai openclaw", true},

		// No recall intent
		{"plain question", "how do I configure the gateway", false},
		{"greeting", "halo, apa kabar", false},
		{"simple task", "tolong buatkan fungsi baru", false},
		{"empty", "", false},
		{"unrelated", "the weather is nice today", false},
		{"only before word", "before I start coding", false},
		{"only after word", "after the meeting", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasRecallIntent(tc.text)
			if got != tc.want {
				t.Errorf("HasRecallIntent(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

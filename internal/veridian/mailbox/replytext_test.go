package mailbox

import "testing"

func TestReplyText(t *testing.T) {
	for in, want := range map[string]string{
		"Bonjour,\r\n\r\n\r\nOui  \r\n> ancien\r\n  > encore\r\nMerci": "Bonjour,\n\nOui\nMerci",
		"> tout cité\n>": "",
		"":               "",
	} {
		if got := ReplyText(in, 100); got != want {
			t.Errorf("%q : %q, attendu %q", in, got, want)
		}
	}
	if got := ReplyText("éééé", 2); got != "éé…" {
		t.Errorf("coupe : %q", got)
	}
}

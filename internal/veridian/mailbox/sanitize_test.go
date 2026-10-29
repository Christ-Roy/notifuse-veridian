package mailbox

import (
	"strings"
	"testing"
)

func TestSanitizeHTML_StripsDangerous(t *testing.T) {
	in := `<p onclick="x()">Bonjour</p><script>alert(1)</script><iframe src="https://evil"></iframe>` +
		`<a href="javascript:alert(1)">pire</a><a href="https://ok.fr/x">bon</a>` +
		`<img src="https://t.example/p.gif" onerror="x()"><form action="/x"><input name="a"></form>` +
		`<div style="color:red;background-color:url(https://evil/x.png);background-image:url(https://evil/y.png);position:fixed">s</div>` +
		`<style>body{background:url(https://evil)}</style><object data="x"></object><meta http-equiv="refresh" content="0;url=https://evil">`
	out := SanitizeHTML(in, nil)
	for _, bad := range []string{"<script", "alert(1)", "onclick", "onerror", "<iframe", "javascript:", "<form", "<input", "url(", "position", "<style", "<object", "<meta", "evil/x.png"} {
		if strings.Contains(out, bad) {
			t.Errorf("le HTML assaini contient encore %q: %s", bad, out)
		}
	}
	for _, good := range []string{"Bonjour", `href="https://ok.fr/x"`, "color: red", `src="https://t.example/p.gif"`, `rel="nofollow`} {
		if !strings.Contains(out, good) {
			t.Errorf("le HTML assaini a perdu %q: %s", good, out)
		}
	}
}

func TestSanitizeHTML_CIDInlined(t *testing.T) {
	cids := map[string]InlineImage{"logo@cabinet": {ContentType: "image/png", Data: []byte("PNGDATA")}}
	out := SanitizeHTML(`<img src="cid:logo@cabinet"><img src="cid:absent">`, cids)
	if !strings.Contains(out, `src="data:image/png;base64,`) {
		t.Fatalf("cid non remplacé: %s", out)
	}
	if strings.Contains(out, "cid:") {
		t.Fatalf("cid résiduel: %s", out)
	}
	// un type non image ne devient jamais une URI data
	out = SanitizeHTML(`<img src="cid:x">`, map[string]InlineImage{"x": {ContentType: "text/html", Data: []byte("<script>")}})
	if strings.Contains(out, "data:") {
		t.Fatalf("data: pour un type non image: %s", out)
	}
}

func TestTextToHTMLEscapes(t *testing.T) {
	out := TextToHTML(`<script>x</script> voir https://a.fr/b?c=1&d=2`)
	if strings.Contains(out, "<script>") || !strings.Contains(out, `href="https://a.fr/b?c=1&amp;d=2"`) {
		t.Fatal(out)
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText(`<p>Bonjour&nbsp;Marie</p><p>Suite<br>ligne</p>`)
	if !strings.Contains(got, "Bonjour") || !strings.Contains(got, "Suite\nligne") {
		t.Fatalf("%q", got)
	}
}

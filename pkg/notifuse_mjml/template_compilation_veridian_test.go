package notifuse_mjml

import (
	"strings"
	"testing"
)

// Veridian fork — tests du découplage pixel d'ouverture / réécriture de liens.
// Le pixel s'insère via /t/ (ou /opens fallback) avant </body> ; la réécriture
// de liens wrappe les href dans /r/ (ou /visit). EnableOpenPixel (*bool) découple
// les deux : nil = comportement upstream (pixel suit EnableTracking), non-nil =
// override explicite (cf. domain.VeridianResolveOpenPixel).

func vbool(b bool) *bool { return &b }

const veridianTrackHTML = `<!DOCTYPE html><html><body><a href="https://example.com">Voir</a></body></html>`

func hasPixel(html string) bool { return strings.Contains(html, "/t/") || strings.Contains(html, "/opens") }
func hasLinkRewrite(html string) bool {
	return strings.Contains(html, "/r/") || strings.Contains(html, "/visit")
}

func TestTrackLinks_EnableTrackingSeul_ComportementUpstream(t *testing.T) {
	// NON-RÉGRESSION : EnableOpenPixel nil + EnableTracking true → pixel ET liens
	// (exactement comme upstream avant le fork).
	out, err := TrackLinks(veridianTrackHTML, TrackingSettings{
		EnableTracking: true, Endpoint: "https://t.example.com", WorkspaceID: "w", MessageID: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPixel(out) {
		t.Error("EnableTracking=true (pixel nil) : pixel attendu (non-régression upstream)")
	}
	if !hasLinkRewrite(out) {
		t.Error("EnableTracking=true : réécriture de liens attendue")
	}
}

func TestTrackLinks_TrackingOff_AucunPixelNiLien(t *testing.T) {
	// NON-RÉGRESSION : tout off, pixel nil → HTML inchangé.
	out, err := TrackLinks(veridianTrackHTML, TrackingSettings{EnableTracking: false})
	if err != nil {
		t.Fatal(err)
	}
	if hasPixel(out) || hasLinkRewrite(out) {
		t.Errorf("tout off : ni pixel ni redirect attendus, got %q", out)
	}
}

func TestTrackLinks_PixelOFF_google_LiensON(t *testing.T) {
	// Cas tunnel google : pixel OFF (EnableOpenPixel=false) MAIS clics ON
	// (EnableTracking=true). Le mail garde le tracking de liens sans le pixel.
	out, err := TrackLinks(veridianTrackHTML, TrackingSettings{
		EnableTracking: true, EnableOpenPixel: vbool(false),
		Endpoint: "https://t.example.com", WorkspaceID: "w", MessageID: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasPixel(out) {
		t.Error("pixel OFF (google) : aucun pixel /t/ attendu")
	}
	if !hasLinkRewrite(out) {
		t.Error("clics ON : réécriture de liens attendue malgré pixel OFF")
	}
}

func TestTrackLinks_PixelON_freemail_SansReecritureLiens(t *testing.T) {
	// Cas pixel forcé ON alors que EnableTracking=false : le pixel doit
	// s'insérer même sans réécriture de liens (le early-return ne doit pas
	// shunter le pixel). Couvre le fix du early-return.
	out, err := TrackLinks(veridianTrackHTML, TrackingSettings{
		EnableTracking: false, EnableOpenPixel: vbool(true),
		Endpoint: "https://t.example.com", WorkspaceID: "w", MessageID: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPixel(out) {
		t.Error("pixel ON forcé : pixel /t/ attendu même sans réécriture de liens")
	}
	if hasLinkRewrite(out) {
		t.Error("EnableTracking=false : pas de réécriture de liens attendue")
	}
}

func TestOpenPixelEnabled(t *testing.T) {
	cases := []struct {
		name    string
		track   bool
		pixel   *bool
		want    bool
	}{
		{"nil suit tracking ON", true, nil, true},
		{"nil suit tracking OFF", false, nil, false},
		{"override true force ON malgré tracking OFF", false, vbool(true), true},
		{"override false force OFF malgré tracking ON", true, vbool(false), false},
	}
	for _, c := range cases {
		got := TrackingSettings{EnableTracking: c.track, EnableOpenPixel: c.pixel}.openPixelEnabled()
		if got != c.want {
			t.Errorf("%s : openPixelEnabled()=%v, attendu %v", c.name, got, c.want)
		}
	}
}

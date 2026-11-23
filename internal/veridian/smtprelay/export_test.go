package smtprelay

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
)

func TestBuildRequestAndWireLocalPart(t *testing.T) {
	if WireLocalPart("abc@poste.local") != "abc" || WireLocalPart("sans-domaine") != "sans-domaine" {
		t.Fatal("partie locale")
	}
	r := BuildRequest("ws", "p", &domain.EmailProvider{}, &Submission{HeaderFrom: "a@b.fr", Subject: "S", Text: "bonjour", InReplyTo: "<x@y>"}, "c@d.fr", "id1")
	if r.WorkspaceID != "ws" || r.IntegrationID != "p" || r.To != "c@d.fr" || r.MessageID != "id1" || r.EmailOptions.InReplyTo != "<x@y>" || r.Content == "" {
		t.Fatalf("%+v", r)
	}
}

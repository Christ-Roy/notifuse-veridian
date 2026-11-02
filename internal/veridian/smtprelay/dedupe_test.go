package smtprelay

import (
	"testing"
	"time"
)

func TestNewDedupe(t *testing.T) {
	d := NewDedupe(time.Hour)
	if d == nil || d.seen == nil || d.ttl != time.Hour {
		t.Fatalf("NewDedupe mal construit: %+v", d)
	}
}

func TestDedupe_SeenRemember(t *testing.T) {
	d := NewDedupe(time.Hour)
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if d.Seen("ws|<a@x>", now) {
		t.Fatal("cle jamais vue ne doit pas etre Seen")
	}
	d.Remember("ws|<a@x>", now)
	if !d.Seen("ws|<a@x>", now.Add(30*time.Minute)) {
		t.Fatal("cle memorisee dans la fenetre doit etre Seen")
	}
	if d.Seen("ws|<a@x>", now.Add(2*time.Hour)) {
		t.Fatal("cle expiree (hors fenetre) ne doit plus etre Seen")
	}
}

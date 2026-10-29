package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

const testKey = "backfill-test-key-0123456789abcdef"

func TestEncryptRowRoundTripHidesPlaintext(t *testing.T) {
	orig := `{"data":{"code":"738291"},"metadata":{"k":"v"}}`
	out, err := encryptRow(orig, testKey)
	if err != nil {
		t.Fatalf("encryptRow: %v", err)
	}
	if strings.Contains(out, "738291") || !strings.Contains(out, "_encrypted") {
		t.Fatalf("sortie non chiffrée: %s", out)
	}
	if err := verifyRow(orig, out, testKey); err != nil {
		t.Fatalf("verifyRow: %v", err)
	}
	var m struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil || m.Metadata["k"] != "v" {
		t.Fatalf("metadata non conservée: %v", err)
	}
}

func TestVerifyRowRejectsWrongKey(t *testing.T) {
	orig := `{"data":{"code":"1"}}`
	out, err := encryptRow(orig, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyRow(orig, out, "autre-cle-0123456789abcdef0123"); err == nil {
		t.Fatal("une mauvaise clé doit échouer la vérification")
	}
}

func TestEncryptRowMatchesRepositoryFormat(t *testing.T) {
	out, err := encryptRow(`{"data":{"a":1}}`, testKey)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptFromHexString(m.Data["_encrypted"], testKey)
	if err != nil || plain != `{"a":1}` {
		t.Fatalf("format attendu {\"_encrypted\": hex(json(data))}, obtenu %q err=%v", plain, err)
	}
}

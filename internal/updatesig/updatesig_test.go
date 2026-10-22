package updatesig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "Plots-update.exe")
	body := []byte("pretend this is Plots.exe")
	if err := os.WriteFile(exe, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	return pub, priv, exe, hex.EncodeToString(h[:])
}

func TestRoundTrip(t *testing.T) {
	pub, priv, exe, sum := setup(t)
	sig := Format(priv, "v0.3.9", sum)
	v, err := VerifyFile(pub, exe, sig)
	if err != nil || v != "0.3.9" {
		t.Fatalf("got %q, %v", v, err)
	}
	k, err := PublicKey(base64.StdEncoding.EncodeToString(pub))
	if err != nil || !bytes.Equal(k, pub) {
		t.Fatal("public key round trip failed")
	}
}

func TestRejects(t *testing.T) {
	pub, priv, exe, sum := setup(t)
	good := Format(priv, "0.3.9", sum)

	// another key
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyFile(other, exe, good); err == nil {
		t.Error("accepted a signature from another key")
	}
	// version changed after signing (e.g. an old build relabelled as new)
	if _, err := VerifyFile(pub, exe, bytes.Replace(good, []byte("0.3.9"), []byte("9.9.9"), 1)); err == nil {
		t.Error("accepted an edited version")
	}
	// the exe changed after signing
	os.WriteFile(exe, []byte("something else"), 0o644)
	if _, err := VerifyFile(pub, exe, good); err == nil {
		t.Error("accepted a file that doesn't match the hash")
	}
	for _, bad := range [][]byte{nil, []byte("hello"), bytes.Repeat([]byte("x"), 5000), good[:len(good)-20]} {
		if _, err := Verify(pub, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := PublicKey(""); err == nil {
		t.Error("accepted an empty public key")
	}
}

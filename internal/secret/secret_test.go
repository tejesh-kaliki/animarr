package secret

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripTamperAndWrongKey(t *testing.T) {
	key := bytes32(1)
	box := NewBox(key)
	stored, err := box.Encrypt("JELLYFIN_API_KEY", "jf-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) < 4 || stored[:3] != "v1:" {
		t.Fatalf("ciphertext %q", stored)
	}
	got, err := box.Decrypt("JELLYFIN_API_KEY", stored)
	if err != nil || got != "jf-secret-value" {
		t.Fatalf("round trip %q %v", got, err)
	}
	if _, err := box.Decrypt("QB_PASSWORD", stored); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("swapped name: %v", err)
	}

	raw, err := base64.RawStdEncoding.DecodeString(stored[3:])
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := "v1:" + base64.RawStdEncoding.EncodeToString(raw)
	if _, err := box.Decrypt("JELLYFIN_API_KEY", tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tamper: %v", err)
	}
	other := NewBox(bytes32(2))
	if _, err := other.Decrypt("JELLYFIN_API_KEY", stored); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := box.Decrypt("JELLYFIN_API_KEY", "v2:aaaa"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("version: %v", err)
	}
}

func TestParseKeyMaterial(t *testing.T) {
	raw := bytes32(9)
	b64 := base64.StdEncoding.EncodeToString(raw[:])
	hx := hex.EncodeToString(raw[:])
	fromB64, err := ParseKey(b64)
	if err != nil || fromB64 != raw {
		t.Fatalf("base64 %v %v", fromB64, err)
	}
	if MaterialKind(b64) != "base64" {
		t.Fatalf("kind %s", MaterialKind(b64))
	}
	fromHex, err := ParseKey(hx)
	if err != nil || fromHex != raw {
		t.Fatalf("hex %v %v", fromHex, err)
	}
	if MaterialKind(hx) != "hex" {
		t.Fatalf("hex kind %s", MaterialKind(hx))
	}
	a, err := ParseKey("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseKey("correct horse battery")
	if err != nil || a != b {
		t.Fatal("passphrase should be stable")
	}
	c, err := ParseKey("another passphrase")
	if err != nil || a == c {
		t.Fatal("different passphrases collided")
	}
	if MaterialKind("correct horse battery") != "passphrase" {
		t.Fatal(MaterialKind("correct horse battery"))
	}
}

func TestKeyFileCreateModeAndEnvPrecedence(t *testing.T) {
	t.Setenv(EnvKey, "")
	dir := t.TempDir()
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "file" || loaded.Warning != "" {
		t.Fatalf("%+v", loaded)
	}
	path := filepath.Join(dir, FileName)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	info, err := os.ReadFile(path)
	if err != nil || len(info) != 32 {
		t.Fatalf("len %d %v", len(info), err)
	}

	loose := t.TempDir()
	loosePath := filepath.Join(loose, FileName)
	fileKey := bytes32(4)
	if err := os.WriteFile(loosePath, fileKey[:], 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(loose)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Warning == "" {
		t.Fatal("expected a permissions warning")
	}
	if loaded.Box.Key() != fileKey {
		t.Fatal("loose file should still be the key, with a warning")
	}

	envKey := bytes32(5)
	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString(envKey[:]))
	loaded, err = Load(loose)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != "env" || loaded.Box.Key() != envKey {
		t.Fatalf("env should win: source %s", loaded.Source)
	}
}

func TestRedact(t *testing.T) {
	got := Redact("jellyfin failed for super-secret-key", "super-secret-key", "ab")
	if got != "jellyfin failed for [redacted]" {
		t.Fatal(got)
	}
}

func bytes32(b byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = b
	}
	return key
}

func TestCorruptKeyFileDoesNotReplaceIt(t *testing.T) {
	t.Setenv(EnvKey, "")
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Warning == "" {
		t.Fatal("expected warning")
	}
	left, err := os.ReadFile(path)
	if err != nil || string(left) != "nope" {
		t.Fatalf("file changed: %q %v", left, err)
	}
	stored, err := NewBox(bytes32(3)).Encrypt("JELLYFIN_API_KEY", "keep-me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.Box.Decrypt("JELLYFIN_API_KEY", stored); err == nil {
		t.Fatal("ephemeral key should not open another key's ciphertext")
	}
}

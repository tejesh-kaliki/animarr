package secret

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	// EnvKey is the environment variable that holds key material.
	// A 32-byte key may be standard or raw base64, or 64 hex characters.
	// Anything else is a passphrase stretched with scrypt.
	EnvKey   = "ANIMARR_SECRET_KEY"
	FileName = "secret.key"

	scryptN    = 1 << 15
	scryptR    = 8
	scryptP    = 1
	scryptSalt = "animarr.settings.v1"
)

// Loaded is the key the process will use.
type Loaded struct {
	Box     *Box
	Source  string // "env" or "file"
	Path    string
	Warning string
}

// ParseKey accepts 32 raw bytes as base64 or hex, or derives a key from a passphrase.
// A string that decodes to exactly 32 bytes is the raw key. A passphrase is not stored.
func ParseKey(material string) ([32]byte, error) {
	s := strings.TrimSpace(material)
	if s == "" {
		return [32]byte{}, errors.New("empty key")
	}
	if key, ok := decodeRaw(s); ok {
		return key, nil
	}
	dk, err := scrypt.Key([]byte(s), []byte(scryptSalt), scryptN, scryptR, scryptP, 32)
	if err != nil {
		return [32]byte{}, err
	}
	var key [32]byte
	copy(key[:], dk)
	return key, nil
}

// MaterialKind reports how ParseKey will read material: "base64", "hex", or "passphrase".
func MaterialKind(material string) string {
	s := strings.TrimSpace(material)
	if len(s) == 64 && isHex(s) {
		if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
			return "hex"
		}
	}
	if _, ok := decodeRaw(s); ok {
		return "base64"
	}
	return "passphrase"
}

func decodeRaw(s string) ([32]byte, bool) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		b, err := enc.DecodeString(s)
		if err == nil && len(b) == 32 {
			var key [32]byte
			copy(key[:], b)
			return key, true
		}
	}
	if len(s) == 64 && isHex(s) {
		b, err := hex.DecodeString(s)
		if err == nil && len(b) == 32 {
			var key [32]byte
			copy(key[:], b)
			return key, true
		}
	}
	return [32]byte{}, false
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return s != ""
}

// Load reads ANIMARR_SECRET_KEY, or the key file under dataPath.
// A missing file is created with mode 0600. A group- or world-readable file is still
// used so the process can start, and Warning says so. A file this function just wrote
// that is not mode 0600 is an error. A corrupt file is left in place; saved secrets
// then fail to decrypt instead of the process exiting.
func Load(dataPath string) (Loaded, error) {
	if v, ok := os.LookupEnv(EnvKey); ok && strings.TrimSpace(v) != "" {
		key, err := ParseKey(v)
		if err != nil {
			return Loaded{}, fmt.Errorf("%s: %w", EnvKey, err)
		}
		return Loaded{Box: NewBox(key), Source: "env"}, nil
	}
	if strings.TrimSpace(dataPath) == "" {
		return Loaded{}, fmt.Errorf("DATA_PATH is empty")
	}
	path := filepath.Join(dataPath, FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			key, gerr := randomKey()
			if gerr != nil {
				return Loaded{}, gerr
			}
			return Loaded{
				Box:     NewBox(key),
				Source:  "file",
				Path:    path,
				Warning: "secret.key could not be read; saved secrets cannot be decrypted",
			}, nil
		}
		key, gerr := randomKey()
		if gerr != nil {
			return Loaded{}, gerr
		}
		if err := WriteKeyFile(path, key); err != nil {
			return Loaded{}, err
		}
		return Loaded{Box: NewBox(key), Source: "file", Path: path}, nil
	}
	warn := modeWarning(path)
	if len(b) != 32 {
		key, gerr := randomKey()
		if gerr != nil {
			return Loaded{}, gerr
		}
		return Loaded{
			Box:     NewBox(key),
			Source:  "file",
			Path:    path,
			Warning: "secret.key is not 32 bytes; it was left in place and saved secrets cannot be decrypted",
		}, nil
	}
	var key [32]byte
	copy(key[:], b)
	return Loaded{Box: NewBox(key), Source: "file", Path: path, Warning: warn}, nil
}

// WriteKeyFile stores a raw 32-byte key with mode 0600.
// It refuses to leave a group- or world-readable file behind.
func WriteKeyFile(path string, key [32]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, key[:], 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("refusing to use %s with mode %o", path, st.Mode().Perm())
	}
	return nil
}

func modeWarning(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "secret.key is readable by group or others; chmod 600 " + path
	}
	return ""
}

func randomKey() ([32]byte, error) {
	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return key, err
	}
	return key, nil
}

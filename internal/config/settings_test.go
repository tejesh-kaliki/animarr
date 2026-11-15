package config

import "testing"

func TestCanonicalRejectsBadValues(t *testing.T) {
	cases := []struct {
		key, value, want string
	}{
		{KeyQBURL, "ftp://qb", "http"},
		{KeyLibraryDir, "relative/path", "absolute"},
		{KeySyncInterval, "soon", "SYNC_INTERVAL"},
		{KeySyncInterval, "0s", "greater than 0"},
		{KeyMinSeeders, "-1", "at least"},
		{KeyMatchThreshold, "2", "at most 1"},
		{KeyPreferredResolution, "999p", "1080p"},
		{KeyNyaaCategory, "1 2", "not allowed"},
		{KeyNFOEnabled, "maybe", "true or false"},
	}
	for _, tc := range cases {
		_, err := Canonical(tc.key, tc.value)
		if err == nil || !contains(err.Error(), tc.want) {
			t.Errorf("%s %q: %v", tc.key, tc.value, err)
		}
	}
	got, err := Canonical(KeySyncInterval, "30m0s")
	if err != nil || got != "30m" {
		t.Fatalf("duration canonical %q %v", got, err)
	}
	got, err = Canonical(KeyNFOEnabled, "off")
	if err != nil || got != "false" {
		t.Fatalf("bool %q %v", got, err)
	}
}

func TestSecretFormatIsEmpty(t *testing.T) {
	cfg := Config{JellyfinAPIKey: "do-not-show", QBPassword: "also-hidden"}
	if Format(cfg, KeyJellyfinAPIKey) != "" || Format(cfg, KeyQBPassword) != "" {
		t.Fatal("format leaked a secret")
	}
	if !SecretSet(cfg, KeyJellyfinAPIKey) || SecretValue(cfg, KeyQBPassword) != "also-hidden" {
		t.Fatal("secret helpers")
	}
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || len(s) > 0 && (containsRest(s, part))))
}

func containsRest(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

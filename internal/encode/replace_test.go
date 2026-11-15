package encode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceKeepsOriginalUntilTempIsInPlace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Demo - S01E01.mp4")
	nfo := filepath.Join(dir, "Demo - S01E01.nfo")
	if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, []byte("<episodedetails></episodedetails>"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := TempPath(src)
	if err := os.WriteFile(tmp, []byte("encoded-video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	final, err := ReplaceVideo(src, tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "Demo - S01E01.mkv")
	if final != want {
		t.Fatalf("final %s", final)
	}
	body, err := os.ReadFile(final)
	if err != nil || string(body) != "encoded-video-bytes" {
		t.Fatalf("mkv %q %v", body, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("original still present: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp still present: %v", err)
	}
	got, err := os.ReadFile(nfo)
	if err != nil || !bytesContains(got, "episodedetails") {
		t.Fatalf("nfo %q %v", got, err)
	}
}

func TestReplaceOverwritesSameMKV(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Demo - S01E02.mkv")
	if err := os.WriteFile(src, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := TempPath(src)
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	final, err := ReplaceVideo(src, tmp)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(final)
	if err != nil || string(body) != "new" || final != src {
		t.Fatalf("%s %q %v", final, body, err)
	}
}

func bytesContains(b []byte, sub string) bool {
	return len(b) > 0 && string(b) != "" && (len(sub) == 0 || contains(string(b), sub))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

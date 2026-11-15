package encode

import (
	"path/filepath"
	"testing"
)

func TestPathMapRoundTrip(t *testing.T) {
	raw := `/data/media/anime=/Volumes/Seagate 4TB/Videos/sonarr/data/media/anime`
	m, err := ParsePathMap(raw)
	if err != nil {
		t.Fatal(err)
	}
	container := "/data/media/anime/Demo Show/Season 01/Demo Show - S01E01.mkv"
	host, err := m.ToHost(container)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/Volumes/Seagate 4TB/Videos/sonarr/data/media/anime", "Demo Show", "Season 01", "Demo Show - S01E01.mkv")
	if host != want {
		t.Fatalf("host %s", host)
	}
	back, err := m.ToContainer(host)
	if err != nil || back != container {
		t.Fatalf("back %s %v", back, err)
	}
	if _, err := m.ToHost("/data/downloads/nope.mkv"); err == nil {
		t.Fatal("path outside the prefix was accepted")
	}
	if _, err := m.ToHost("/data/media/anime/../secrets.mkv"); err == nil {
		t.Fatal("escaped path was accepted")
	}
}

func TestParsePathMapRejectsEmpty(t *testing.T) {
	for _, raw := range []string{"", "only", "=host", "container=", "container"} {
		if _, err := ParsePathMap(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

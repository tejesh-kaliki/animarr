package encode

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathMap translates a container library path to the path on the Mac, and back.
type PathMap struct {
	From string // container prefix, for example /data/media/anime
	To   string // host prefix
}

// ParsePathMap reads container=host. The split is the first equals sign.
func ParsePathMap(raw string) (PathMap, error) {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "=")
	if i <= 0 || i == len(raw)-1 {
		return PathMap{}, fmt.Errorf("--path-map must be container=host")
	}
	from := cleanPrefix(raw[:i])
	to := cleanPrefix(raw[i+1:])
	if from == "" || to == "" || from == "." || to == "." {
		return PathMap{}, fmt.Errorf("--path-map prefixes are empty")
	}
	return PathMap{From: from, To: to}, nil
}

func cleanPrefix(p string) string {
	p = filepath.Clean(strings.TrimSpace(p))
	if p == "/" {
		return p
	}
	return strings.TrimRight(p, `/\`)
}

// ToHost maps a container path onto the host prefix.
func (m PathMap) ToHost(container string) (string, error) {
	rel, err := relative(m.From, container)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return m.To, nil
	}
	return filepath.Join(m.To, filepath.FromSlash(rel)), nil
}

// ToContainer maps a host path back onto the container prefix.
func (m PathMap) ToContainer(host string) (string, error) {
	rel, err := relative(m.To, host)
	if err != nil {
		return "", err
	}
	if rel == "" {
		return m.From, nil
	}
	return m.From + "/" + filepath.ToSlash(rel), nil
}

func relative(prefix, full string) (string, error) {
	prefix = cleanPrefix(prefix)
	full = filepath.Clean(strings.TrimSpace(full))
	if full != prefix && !strings.HasPrefix(full, prefix+string(filepath.Separator)) && !strings.HasPrefix(full, prefix+"/") {
		return "", fmt.Errorf("%s is outside %s", full, prefix)
	}
	rel := strings.TrimPrefix(full, prefix)
	rel = strings.TrimPrefix(rel, "/")
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	if rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", fmt.Errorf("%s escapes %s", full, prefix)
	}
	return rel, nil
}

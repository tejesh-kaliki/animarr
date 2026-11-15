package encode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReplaceVideo moves a verified temp mkv onto the library name.
// The original is removed only after that move succeeds. A sibling .nfo follows an extension change.
func ReplaceVideo(src, tmp string) (string, error) {
	if src == "" || tmp == "" {
		return "", fmt.Errorf("replace path is empty")
	}
	if _, err := os.Stat(tmp); err != nil {
		return "", fmt.Errorf("encoded file: %w", err)
	}
	ext := filepath.Ext(src)
	final := strings.TrimSuffix(src, ext) + ".mkv"
	if filepath.Clean(final) == filepath.Clean(src) {
		if err := os.Rename(tmp, src); err != nil {
			return "", err
		}
		return src, nil
	}
	if _, err := os.Stat(final); err == nil {
		return "", fmt.Errorf("destination exists: %s", final)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
		return final, fmt.Errorf("new file is %s but the original could not be removed: %w", final, err)
	}
	oldNFO := strings.TrimSuffix(src, ext) + ".nfo"
	newNFO := strings.TrimSuffix(final, filepath.Ext(final)) + ".nfo"
	if oldNFO != newNFO {
		if _, err := os.Stat(oldNFO); err == nil {
			if err := os.Rename(oldNFO, newNFO); err != nil {
				return final, fmt.Errorf("renamed video to %s but the nfo was not renamed: %w", final, err)
			}
		}
	}
	return final, nil
}

// TempPath is the encode output next to the source. It is not a library name.
func TempPath(src string) string {
	return filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".animarr-encode.mkv")
}

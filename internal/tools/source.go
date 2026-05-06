package tools

import (
	"errors"
	"fmt"
	"os"
)

// MaxFileBytes caps the size of a file the runner is willing to read on
// behalf of a tool. 4 MiB is enough for any source/log/config we expect to
// handle and protects us from /proc/-style files that stream forever.
const MaxFileBytes = 4 * 1024 * 1024

var (
	errNoSource = errors.New("either 'path' or the inline content field must be provided")
)

// loadSource resolves a tool's input. If path is non-empty it reads the file
// (returning fetched=true so stats correctly account for the token saving);
// otherwise it returns the inline content (fetched=false). It's an error to
// supply neither.
//
// Tool input structs can keep their pre-existing inline field (`content`,
// `source`, `input`) and add a `path` field that takes precedence when set.
func loadSource(path, inline string) (content string, fetched bool, err error) {
	if path != "" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", false, fmt.Errorf("stat %s: %w", path, statErr)
		}
		if info.IsDir() {
			return "", false, fmt.Errorf("%s is a directory", path)
		}
		if info.Size() > MaxFileBytes {
			return "", false, fmt.Errorf("%s is %d bytes, exceeds limit %d (read it directly with Read instead)", path, info.Size(), MaxFileBytes)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", false, fmt.Errorf("read %s: %w", path, readErr)
		}
		return string(data), true, nil
	}
	if inline == "" {
		return "", false, errNoSource
	}
	return inline, false, nil
}

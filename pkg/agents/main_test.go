package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates the user config directory so preference tests never write
// into the real ~/.config/bv (H3). The teardown compares the real directory's
// listing before and after the run and fails the package if anything changed.
func TestMain(m *testing.M) {
	realConfig, _ := os.UserConfigDir()
	before := listConfigDir(realConfig)

	tmp, err := os.MkdirTemp("", "bv-agents-test-home-")
	if err != nil {
		panic("creating isolated HOME: " + err.Error())
	}
	os.Setenv("HOME", tmp)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	code := m.Run()

	if after := listConfigDir(realConfig); before != after {
		fmt.Fprintf(os.Stderr, "pkg/agents tests modified the real config dir %s:\nbefore: %s\nafter:  %s\n", realConfig, before, after)
		if code == 0 {
			code = 1
		}
	}
	os.RemoveAll(tmp)
	os.Exit(code)
}

// listConfigDir returns a stable fingerprint of <configDir>/bv (names and
// sizes of every file, recursively); "" when the directory does not exist.
func listConfigDir(configDir string) string {
	if configDir == "" {
		return ""
	}
	root := filepath.Join(configDir, "bv")
	var out string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		out += fmt.Sprintf("%s:%d;", path, info.Size())
		return nil
	})
	return out
}

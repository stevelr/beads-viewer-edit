package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateIgnoreEnv neutralizes every environment source EnsureBVIgnored
// consults so tests are hermetic regardless of the developer's real global
// gitignore / git config.
func isolateIgnoreEnv(t *testing.T) {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv(NoGitignoreEnvVar, "")
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(isolated, "nonexistent-gitconfig"))
}

// makeGitDir creates a plain .git directory inside dir.
func makeGitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(content)
}

func TestMatchesBVPattern(t *testing.T) {
	tests := []struct {
		line    string
		matches bool
	}{
		// Should match
		{".bv", true},
		{".bv/", true},
		{".bv/*", true},
		{".bv/**", true},
		{".bv/**/*", true},
		{"/.bv", true}, // Leading slash should be normalized
		{"/.bv/", true},

		// Should not match
		{"", false},
		{"#.bv", false}, // Comment
		{".bv2", false},
		{".bvx", false},
		{"bv/", false},
		{".beads/", false},
		{"node_modules/", false},
		{".bv-backup", false},
		{"*.bv", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := matchesBVPattern(tt.line)
			if got != tt.matches {
				t.Errorf("matchesBVPattern(%q) = %v, want %v", tt.line, got, tt.matches)
			}
		})
	}
}

func TestIgnoreFileCoversBV(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "empty file",
			content:  "",
			expected: false,
		},
		{
			name:     "has .bv",
			content:  "node_modules/\n.bv\n*.log\n",
			expected: true,
		},
		{
			name:     "has .bv/",
			content:  "node_modules/\n.bv/\n*.log\n",
			expected: true,
		},
		{
			name:     "has .bv/*",
			content:  ".bv/*\n",
			expected: true,
		},
		{
			name:     "has /.bv/",
			content:  "/.bv/\n",
			expected: true,
		},
		{
			name:     "commented out",
			content:  "# .bv/\n",
			expected: false,
		},
		{
			name:     "different pattern",
			content:  ".beads/\nnode_modules/\n",
			expected: false,
		},
		{
			name:     "similar but not matching",
			content:  ".bv2/\n.bvx\nbv/\n",
			expected: false,
		},
		{
			name:     "with whitespace",
			content:  "  .bv/  \n",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			gitignorePath := filepath.Join(tmpDir, ".gitignore")

			if err := os.WriteFile(gitignorePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			got, err := ignoreFileCoversBV(gitignorePath)
			if err != nil {
				t.Fatalf("ignoreFileCoversBV() error = %v", err)
			}
			if got != tt.expected {
				t.Errorf("ignoreFileCoversBV() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIgnoreFileCoversBV_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	_, err := ignoreFileCoversBV(gitignorePath)
	if !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist error, got %v", err)
	}
}

func TestAppendToIgnoreFile(t *testing.T) {
	tests := []struct {
		name            string
		existingContent string
		pattern         string
		wantContains    []string
		wantPrefix      string // expected prefix of the file (for checking no leading blank line)
	}{
		{
			name:            "new file",
			existingContent: "",
			pattern:         ".bv/",
			wantContains:    []string{"# bv (beads viewer)", ".bv/"},
			wantPrefix:      "#", // should start with comment, not blank line
		},
		{
			name:            "existing file with newline",
			existingContent: "node_modules/\n",
			pattern:         ".bv/",
			wantContains:    []string{"node_modules/", "# bv (beads viewer)", ".bv/"},
			wantPrefix:      "node_modules/",
		},
		{
			name:            "existing file without trailing newline",
			existingContent: "node_modules/",
			pattern:         ".bv/",
			wantContains:    []string{"node_modules/", "# bv (beads viewer)", ".bv/"},
			wantPrefix:      "node_modules/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			gitignorePath := filepath.Join(tmpDir, ".gitignore")

			// Create existing file if content is provided
			if tt.existingContent != "" {
				if err := os.WriteFile(gitignorePath, []byte(tt.existingContent), 0644); err != nil {
					t.Fatalf("failed to write existing file: %v", err)
				}
			}

			if err := appendToIgnoreFile(gitignorePath, tt.pattern); err != nil {
				t.Fatalf("appendToIgnoreFile() error = %v", err)
			}

			content, err := os.ReadFile(gitignorePath)
			if err != nil {
				t.Fatalf("failed to read result: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(string(content), want) {
					t.Errorf("result missing %q, got:\n%s", want, content)
				}
			}

			// Check prefix (no unexpected leading blank lines)
			if tt.wantPrefix != "" && !strings.HasPrefix(string(content), tt.wantPrefix) {
				t.Errorf("expected file to start with %q, got:\n%s", tt.wantPrefix, content)
			}
		})
	}
}

func TestEnsureBVIgnored(t *testing.T) {
	t.Run("skips non-git directories entirely", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(tmpDir, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore in non-git dir, stat err = %v", err)
		}
	})

	t.Run("writes to .git/info/exclude, not .gitignore", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		exclude := readFileOrEmpty(t, filepath.Join(tmpDir, ".git", "info", "exclude"))
		if !strings.Contains(exclude, ".bv/") {
			t.Errorf("expected .bv/ in .git/info/exclude, got:\n%s", exclude)
		}
		if _, err := os.Stat(filepath.Join(tmpDir, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore to be created, stat err = %v", err)
		}
	})

	t.Run("appends to existing exclude without losing content", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)
		excludePath := filepath.Join(tmpDir, ".git", "info", "exclude")
		if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(excludePath, []byte("*.swp\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		exclude := readFileOrEmpty(t, excludePath)
		if !strings.Contains(exclude, "*.swp") {
			t.Error("existing exclude content was lost")
		}
		if !strings.Contains(exclude, ".bv/") {
			t.Errorf("expected .bv/ in exclude, got:\n%s", exclude)
		}
	})

	t.Run("respects existing .gitignore entry", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)
		gitignorePath := filepath.Join(tmpDir, ".gitignore")
		if err := os.WriteFile(gitignorePath, []byte(".bv/\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(tmpDir, ".git", "info", "exclude")); !os.IsNotExist(err) {
			t.Errorf("expected no exclude file when .gitignore already covers .bv, stat err = %v", err)
		}
		content := readFileOrEmpty(t, gitignorePath)
		if count := strings.Count(content, ".bv/"); count != 1 {
			t.Errorf("expected exactly 1 occurrence of .bv/ in .gitignore, got %d:\n%s", count, content)
		}
	})

	t.Run("respects existing exclude entry", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)
		excludePath := filepath.Join(tmpDir, ".git", "info", "exclude")
		if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(excludePath, []byte(".bv\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		exclude := readFileOrEmpty(t, excludePath)
		if strings.Contains(exclude, "# bv (beads viewer)") {
			t.Errorf("should not append when exclude already covers .bv, got:\n%s", exclude)
		}
		if _, err := os.Stat(filepath.Join(tmpDir, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore to be created, stat err = %v", err)
		}
	})

	t.Run("idempotent - doesn't duplicate in exclude", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		for i := 0; i < 2; i++ {
			if err := EnsureBVIgnored(tmpDir); err != nil {
				t.Fatalf("EnsureBVIgnored() run %d error = %v", i+1, err)
			}
		}

		exclude := readFileOrEmpty(t, filepath.Join(tmpDir, ".git", "info", "exclude"))
		if count := strings.Count(exclude, ".bv/"); count != 1 {
			t.Errorf("expected exactly 1 occurrence of .bv/, got %d:\n%s", count, exclude)
		}
	})

	t.Run("BV_NO_GITIGNORE opts out of all writes", func(t *testing.T) {
		isolateIgnoreEnv(t)
		t.Setenv(NoGitignoreEnvVar, "1")
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(tmpDir, ".git", "info", "exclude")); !os.IsNotExist(err) {
			t.Errorf("expected no exclude file with %s set, stat err = %v", NoGitignoreEnvVar, err)
		}
		if _, err := os.Stat(filepath.Join(tmpDir, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore with %s set, stat err = %v", NoGitignoreEnvVar, err)
		}
	})

	t.Run("linked worktree writes to shared common-dir exclude", func(t *testing.T) {
		isolateIgnoreEnv(t)
		mainRepo := t.TempDir()
		makeGitDir(t, mainRepo)
		worktreeGitDir := filepath.Join(mainRepo, ".git", "worktrees", "wt")
		if err := os.MkdirAll(worktreeGitDir, 0755); err != nil {
			t.Fatal(err)
		}
		// git writes a relative commondir pointer ("../..") for linked worktrees.
		if err := os.WriteFile(filepath.Join(worktreeGitDir, "commondir"), []byte("../..\n"), 0644); err != nil {
			t.Fatal(err)
		}

		worktree := t.TempDir()
		gitFile := "gitdir: " + worktreeGitDir + "\n"
		if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(gitFile), 0644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureBVIgnored(worktree); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		exclude := readFileOrEmpty(t, filepath.Join(mainRepo, ".git", "info", "exclude"))
		if !strings.Contains(exclude, ".bv/") {
			t.Errorf("expected .bv/ in the main repo's .git/info/exclude, got:\n%s", exclude)
		}
		if _, err := os.Stat(filepath.Join(worktree, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore in the worktree, stat err = %v", err)
		}
	})

	t.Run("respects global excludesFile from git config", func(t *testing.T) {
		isolateIgnoreEnv(t)
		globalDir := t.TempDir()
		globalIgnore := filepath.Join(globalDir, "gitignore_global")
		if err := os.WriteFile(globalIgnore, []byte(".bv/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitconfig := filepath.Join(globalDir, "gitconfig")
		cfg := "[user]\n\tname = Test\n[core]\n\texcludesFile = " + globalIgnore + "\n"
		if err := os.WriteFile(gitconfig, []byte(cfg), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)

		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(tmpDir, ".git", "info", "exclude")); !os.IsNotExist(err) {
			t.Errorf("expected no exclude file when global gitignore covers .bv, stat err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(tmpDir, ".gitignore")); !os.IsNotExist(err) {
			t.Errorf("expected no .gitignore when global gitignore covers .bv, stat err = %v", err)
		}
	})

	t.Run("respects default XDG global ignore", func(t *testing.T) {
		isolateIgnoreEnv(t)
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		if err := os.MkdirAll(filepath.Join(xdg, "git"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xdg, "git", "ignore"), []byte(".bv\n"), 0644); err != nil {
			t.Fatal(err)
		}

		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(tmpDir, ".git", "info", "exclude")); !os.IsNotExist(err) {
			t.Errorf("expected no exclude file when XDG global ignore covers .bv, stat err = %v", err)
		}
	})

	t.Run("falls back to .gitignore when git dir is unresolvable", func(t *testing.T) {
		isolateIgnoreEnv(t)
		tmpDir := t.TempDir()
		// A .git file that is not a valid gitdir pointer.
		if err := os.WriteFile(filepath.Join(tmpDir, ".git"), []byte("not a gitdir pointer\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureBVIgnored(tmpDir); err != nil {
			t.Fatalf("EnsureBVIgnored() error = %v", err)
		}

		content := readFileOrEmpty(t, filepath.Join(tmpDir, ".gitignore"))
		if !strings.Contains(content, ".bv/") {
			t.Errorf("expected fallback .bv/ append to .gitignore, got:\n%s", content)
		}
	})
}

func TestEnsureBVIgnored_UsesCurrentDir(t *testing.T) {
	isolateIgnoreEnv(t)

	// Save current directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	makeGitDir(t, tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Call with empty string - should use current directory
	if err := EnsureBVIgnored(""); err != nil {
		t.Fatalf("EnsureBVIgnored() error = %v", err)
	}

	exclude := readFileOrEmpty(t, filepath.Join(tmpDir, ".git", "info", "exclude"))
	if !strings.Contains(exclude, ".bv/") {
		t.Errorf("expected .bv/ in .git/info/exclude, got:\n%s", exclude)
	}
}

func TestResolveGitCommonDir(t *testing.T) {
	t.Run("plain .git directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		makeGitDir(t, tmpDir)

		got, ok := resolveGitCommonDir(tmpDir, filepath.Join(tmpDir, ".git"), true)
		if !ok {
			t.Fatal("expected resolution to succeed")
		}
		want := filepath.Clean(filepath.Join(tmpDir, ".git"))
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("relative gitdir pointer", func(t *testing.T) {
		tmpDir := t.TempDir()
		realGit := filepath.Join(tmpDir, "actual-git")
		if err := os.MkdirAll(realGit, 0755); err != nil {
			t.Fatal(err)
		}
		project := filepath.Join(tmpDir, "project")
		if err := os.MkdirAll(project, 0755); err != nil {
			t.Fatal(err)
		}
		gitFile := filepath.Join(project, ".git")
		if err := os.WriteFile(gitFile, []byte("gitdir: ../actual-git\n"), 0644); err != nil {
			t.Fatal(err)
		}

		got, ok := resolveGitCommonDir(project, gitFile, false)
		if !ok {
			t.Fatal("expected resolution to succeed")
		}
		if got != filepath.Clean(realGit) {
			t.Errorf("got %q, want %q", got, filepath.Clean(realGit))
		}
	})

	t.Run("invalid pointer file", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitFile := filepath.Join(tmpDir, ".git")
		if err := os.WriteFile(gitFile, []byte("garbage\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, ok := resolveGitCommonDir(tmpDir, gitFile, false); ok {
			t.Error("expected resolution to fail for invalid pointer file")
		}
	})

	t.Run("dangling gitdir pointer", func(t *testing.T) {
		tmpDir := t.TempDir()
		gitFile := filepath.Join(tmpDir, ".git")
		if err := os.WriteFile(gitFile, []byte("gitdir: /nonexistent/path/.git\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, ok := resolveGitCommonDir(tmpDir, gitFile, false); ok {
			t.Error("expected resolution to fail for dangling pointer")
		}
	})
}

func TestParseExcludesFile(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantValue string
		wantFound bool
	}{
		{
			name:      "simple",
			content:   "[core]\n\texcludesFile = /tmp/ignore\n",
			wantValue: "/tmp/ignore",
			wantFound: true,
		},
		{
			name:      "case-insensitive key and section",
			content:   "[CORE]\nEXCLUDESFILE=/tmp/ignore\n",
			wantValue: "/tmp/ignore",
			wantFound: true,
		},
		{
			name:      "quoted value",
			content:   "[core]\n\texcludesfile = \"/tmp/my ignore\"\n",
			wantValue: "/tmp/my ignore",
			wantFound: true,
		},
		{
			name:      "outside core section",
			content:   "[user]\n\texcludesfile = /tmp/ignore\n",
			wantValue: "",
			wantFound: false,
		},
		{
			name:      "last occurrence wins",
			content:   "[core]\n\texcludesfile = /tmp/first\n\texcludesfile = /tmp/second\n",
			wantValue: "/tmp/second",
			wantFound: true,
		},
		{
			name:      "no core section",
			content:   "[user]\n\tname = Test\n",
			wantValue: "",
			wantFound: false,
		},
		{
			name:      "comments ignored",
			content:   "[core]\n# excludesfile = /tmp/commented\n; excludesfile = /tmp/also-commented\n",
			wantValue: "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "gitconfig")
			if err := os.WriteFile(cfgPath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			value, found := parseExcludesFile(cfgPath)
			if found != tt.wantFound || value != tt.wantValue {
				t.Errorf("parseExcludesFile() = (%q, %v), want (%q, %v)", value, found, tt.wantValue, tt.wantFound)
			}
		})
	}
}

func TestExpandUserPath(t *testing.T) {
	if got := expandUserPath("~/ignore", "/home/x"); got != filepath.Join("/home/x", "ignore") {
		t.Errorf("expandUserPath(~/ignore) = %q", got)
	}
	if got := expandUserPath("/abs/ignore", "/home/x"); got != "/abs/ignore" {
		t.Errorf("expandUserPath(/abs/ignore) = %q", got)
	}
	if got := expandUserPath("~/ignore", ""); got != "~/ignore" {
		t.Errorf("expandUserPath with empty home = %q", got)
	}
}

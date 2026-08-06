package version

import "testing"

func TestIsUsableVersion(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{" ", false},
		{"v", false},
		{"v.", false},
		{"  v  ", false},
		{"v0.14.4", true},
		{"0.14.4", true},
		{"v1.0.0", true},
		{"1.0.0-rc1", true},
		{"v0.14.5-0.20260212-abcdef", true}, // pseudo-version is still "usable" at this layer
		// Un-substituted release-pipeline templates must be rejected (#174) so
		// resolution falls through to build-info / the hardcoded fallback
		// instead of accepting a literal placeholder.
		{"${version}", false},
		{"v${version}", false},
		{"{{.Version}}", false},
		{"v{{.Version}}", false},
		{" v${version} ", false},
	}
	for _, tt := range tests {
		if got := isUsableVersion(tt.input); got != tt.want {
			t.Errorf("isUsableVersion(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0.14.4", "v0.14.4"},
		{"v0.14.4", "v0.14.4"},
		{" v0.14.4 ", "v0.14.4"},
		{" 1.0.0 ", "v1.0.0"},
	}
	for _, tt := range tests {
		if got := normalizeVersion(tt.input); got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVersionIsNeverEmpty(t *testing.T) {
	// After init() runs, Version must never be empty.
	if Version == "" {
		t.Fatal("Version is empty after init(); this is the #126 bug")
	}
}

func TestFallbackHasVPrefix(t *testing.T) {
	if fallback[0] != 'v' {
		t.Errorf("fallback constant %q must start with 'v'", fallback)
	}
}

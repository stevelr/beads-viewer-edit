package ui

import (
	"strings"
	"testing"
)

func TestGitRemoteToWebURL(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{
			name:   "scp style github",
			remote: "git@github.com:owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "ssh URL github",
			remote: "ssh://git@github.com/owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "ssh URL gitlab nested group",
			remote: "ssh://git@gitlab.com/group/subgroup/repo.git",
			want:   "https://gitlab.com/group/subgroup/repo",
		},
		{
			name:   "ssh URL drops ssh port",
			remote: "ssh://git@github.com:2222/owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "https trims suffix and query",
			remote: "https://github.com/owner/repo.git?ignored=1",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "empty path rejected",
			remote: "ssh://git@github.com",
			want:   "",
		},
		{
			name:   "unsupported scheme rejected",
			remote: "file:///tmp/repo.git",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitRemoteToWebURL(tt.remote)
			if strings.Compare(got, tt.want) != 0 {
				t.Fatalf("gitRemoteToWebURL(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}

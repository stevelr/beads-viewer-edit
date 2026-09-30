package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func rewriteGitHubDownloadsToServer(t *testing.T, client *http.Client, serverURL string) {
	t.Helper()
	target, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clonedURL := *req.URL
		clone.URL = &clonedURL
		if req.URL.Hostname() == "github.com" && strings.Contains(req.URL.Path, "/releases/download/") {
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			if strings.HasSuffix(req.URL.Path, "/checksums.txt") {
				clone.URL.Path = "/checksums"
			}
		}
		return base.RoundTrip(clone)
	})
}

// TestIsGitHubHost verifies the domain allow-list for token transmission.
func TestIsGitHubHost(t *testing.T) {
	tests := []struct {
		rawURL string
		want   bool
	}{
		{"https://api.github.com/repos/foo/bar", true},
		{"https://github.com/releases/download/v1.0/file.tar.gz", true},
		{"https://objects.githubusercontent.com/something", true},
		{"https://raw.githubusercontent.com/foo/bar/main/file", true},
		{"https://githubusercontent.com/something", true},
		{"https://evil.com", false},
		{"https://notgithub.com/repos", false},
		{"https://github.com.evil.com/phish", false},
		{"http://localhost:8080/test", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.rawURL, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.rawURL, nil)
			if err != nil {
				t.Fatalf("bad test URL %q: %v", tt.rawURL, err)
			}
			if got := isGitHubHost(req.URL); got != tt.want {
				t.Errorf("isGitHubHost(%q) = %v, want %v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestCheckForUpdates_Network(t *testing.T) {
	// We cannot replace version.Version without link-time substitution, so the
	// fixtures use an obviously newer tag and an obviously older tag.

	validChecksumBody := strings.TrimPrefix(testSHA256DigestA, "sha256:") + "  " + stableAssetName() + "\n"
	installableReleaseBody := func(tag, checksumBody string) string {
		checksumHash := sha256.Sum256([]byte(checksumBody))
		release := Release{
			TagName: tag,
			HTMLURL: "https://github.com/stevelr/beads-viewer-edit/releases/tag/" + tag,
			Assets: []Asset{
				{
					Name:               stableAssetName(),
					BrowserDownloadURL: "https://github.com/stevelr/beads-viewer-edit/releases/download/" + tag + "/" + stableAssetName(),
					Size:               1024,
					Digest:             testSHA256DigestA,
					State:              "uploaded",
				},
				{
					Name:               "checksums.txt",
					BrowserDownloadURL: "https://github.com/stevelr/beads-viewer-edit/releases/download/" + tag + "/checksums.txt",
					Size:               int64(len(checksumBody)),
					Digest:             "sha256:" + hex.EncodeToString(checksumHash[:]),
					State:              "uploaded",
				},
			},
		}
		data, err := json.Marshal(release)
		if err != nil {
			t.Fatalf("marshal release fixture: %v", err)
		}
		return string(data)
	}

	tests := []struct {
		name           string
		responseBody   string
		responseStatus int
		expectTag      string
		expectURL      string
		expectErr      bool
		checksumBody   string
	}{
		{
			name:           "Newer version available",
			responseBody:   installableReleaseBody("v99.0.0", validChecksumBody),
			responseStatus: http.StatusOK,
			expectTag:      "v99.0.0",
			expectURL:      "https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0",
			expectErr:      false,
			checksumBody:   validChecksumBody,
		},
		{
			name:           "Newer release manifest disagrees with asset digest",
			responseBody:   installableReleaseBody("v99.0.0", strings.TrimPrefix(testSHA256DigestB, "sha256:")+"  "+stableAssetName()+"\n"),
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
			checksumBody:   strings.TrimPrefix(testSHA256DigestB, "sha256:") + "  " + stableAssetName() + "\n",
		},
		{
			name:           "Same version (no update)",
			responseBody:   `{"tag_name":"v0.0.0","html_url":"https://github.com/stevelr/beads-viewer-edit/releases/tag/v0.0.0"}`, // Assumes current > v0.0.0
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      false,
		},
		{
			name:           "Rate limit (403)",
			responseBody:   `{"message": "rate limit exceeded"}`,
			responseStatus: http.StatusForbidden,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Rate limit (429)",
			responseBody:   `{"message":"rate limit exceeded"}`,
			responseStatus: http.StatusTooManyRequests,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Server error (500)",
			responseBody:   "",
			responseStatus: http.StatusInternalServerError,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Invalid JSON",
			responseBody:   `{invalid json}`,
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Invalid release tag",
			responseBody:   `{"tag_name":"v99.0.0.1","html_url":"https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0.1"}`,
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Newer release missing platform asset",
			responseBody:   `{"tag_name":"v99.0.0","html_url":"https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0","assets":[{"name":"checksums.txt","browser_download_url":"https://github.com/stevelr/beads-viewer-edit/releases/download/v99.0.0/checksums.txt","size":256,"digest":"` + testSHA256DigestB + `","state":"uploaded"}]}`,
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
		{
			name:           "Newer release missing checksum asset",
			responseBody:   `{"tag_name":"v99.0.0","html_url":"https://github.com/stevelr/beads-viewer-edit/releases/tag/v99.0.0","assets":[{"name":"` + stableAssetName() + `","browser_download_url":"https://github.com/stevelr/beads-viewer-edit/releases/download/v99.0.0/` + stableAssetName() + `","size":1024,"digest":"` + testSHA256DigestA + `","state":"uploaded"}]}`,
			responseStatus: http.StatusOK,
			expectTag:      "",
			expectURL:      "",
			expectErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/checksums" {
					w.WriteHeader(http.StatusOK)
					if _, err := w.Write([]byte(tt.checksumBody)); err != nil {
						t.Errorf("write checksum response: %v", err)
					}
					return
				}
				if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
					t.Errorf("Accept header = %q", got)
				}
				if got := r.Header.Get("X-GitHub-Api-Version"); got != githubAPIVersion {
					t.Errorf("X-GitHub-Api-Version header = %q", got)
				}
				w.WriteHeader(tt.responseStatus)
				if _, err := w.Write([]byte(tt.responseBody)); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer server.Close()

			client := server.Client()
			client.Timeout = 1 * time.Second
			rewriteGitHubDownloadsToServer(t, client, server.URL)

			tag, url, err := checkForUpdates(client, server.URL)

			if (err != nil) != tt.expectErr {
				t.Errorf("checkForUpdates() error = %v, expectErr %v", err, tt.expectErr)
				return
			}

			if tag != tt.expectTag {
				t.Errorf("checkForUpdates() tag = %v, want %v", tag, tt.expectTag)
			}
			if url != tt.expectURL {
				t.Errorf("checkForUpdates() url = %v, want %v", url, tt.expectURL)
			}
		})
	}
}

// isolateUpdaterConfig points the updater at an empty config directory and
// clears every policy-related environment variable so a test starts from the
// documented defaults regardless of the developer's real ~/.config/bv.
func isolateUpdaterConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv(envNoSavedConfig, "")
	t.Setenv(EnvNoUpdateCheck, "")
	t.Setenv(EnvUseToken, "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	return filepath.Join(dir, "bv")
}

// TestSetGitHubAuth_GitHubDomain verifies that an ambient GITHUB_TOKEN /
// GH_TOKEN is sent as a Bearer token only when the user opted in
// (BV_UPDATE_USE_TOKEN=1) and only for GitHub domains (#117, #197).
func TestSetGitHubAuth_GitHubDomain(t *testing.T) {
	tests := []struct {
		name     string
		envVar   string
		envVal   string
		optIn    bool
		url      string
		wantAuth string
	}{
		{"GITHUB_TOKEN set + opted in + GitHub URL", "GITHUB_TOKEN", "ghp_test123", true, "https://api.github.com/repos/foo/bar", "Bearer ghp_test123"},
		{"GH_TOKEN set + opted in + GitHub URL", "GH_TOKEN", "gho_fallback456", true, "https://api.github.com/repos/foo/bar", "Bearer gho_fallback456"},
		{"GITHUB_TOKEN set + NOT opted in + GitHub URL", "GITHUB_TOKEN", "ghp_test123", false, "https://api.github.com/repos/foo/bar", ""},
		{"No token set + opted in + GitHub URL", "", "", true, "https://api.github.com/repos/foo/bar", ""},
		{"GITHUB_TOKEN set + opted in + non-GitHub URL", "GITHUB_TOKEN", "ghp_test123", true, "https://example.com/download", ""},
		{"GITHUB_TOKEN set + opted in + localhost URL", "GITHUB_TOKEN", "ghp_test123", true, "http://localhost:8080/test", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateUpdaterConfig(t)
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envVal)
			}
			if tt.optIn {
				t.Setenv(EnvUseToken, "1")
			}

			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			setGitHubAuth(req)

			gotAuth := req.Header.Get("Authorization")
			if gotAuth != tt.wantAuth {
				t.Errorf("Authorization header = %q, want %q", gotAuth, tt.wantAuth)
			}
		})
	}
}

// TestGitHubToken_Precedence verifies GITHUB_TOKEN takes precedence over
// GH_TOKEN, both for the raw environment lookup and for the policy-gated
// token once the user has opted in.
func TestGitHubToken_Precedence(t *testing.T) {
	isolateUpdaterConfig(t)
	t.Setenv("GITHUB_TOKEN", "primary")
	t.Setenv("GH_TOKEN", "fallback")

	if tok := ambientGitHubToken(); tok != "primary" {
		t.Errorf("ambientGitHubToken() = %q, want %q (GITHUB_TOKEN should take precedence)", tok, "primary")
	}
	if tok := githubToken(); tok != "" {
		t.Errorf("githubToken() = %q without opt-in, want empty", tok)
	}

	t.Setenv(EnvUseToken, "1")
	if tok := githubToken(); tok != "primary" {
		t.Errorf("githubToken() = %q after opt-in, want %q (GITHUB_TOKEN should take precedence)", tok, "primary")
	}
}

// rewriteAPIToServer routes every request through the test server while
// leaving req.URL (and therefore the GitHub host check) untouched, and
// records the headers the updater actually put on the wire.
func rewriteAPIToServer(t *testing.T, serverURL string, seen *http.Header) *http.Client {
	t.Helper()
	target, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*seen = req.Header.Clone()
			clone := req.Clone(req.Context())
			clonedURL := *req.URL
			clone.URL = &clonedURL
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}
}

// olderReleaseServer serves a release that is never newer than the running
// binary, so checkForUpdates completes without needing checksum fixtures.
func olderReleaseServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tag_name":"v0.0.1","html_url":"https://github.com/stevelr/beads-viewer-edit/releases/tag/v0.0.1","assets":[]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func writeUpdaterConfig(t *testing.T, configDir, body string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, userConfigFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
}

// TestUpdater_OptOutEnvAndConfig covers the startup-check opt-outs: the
// default is on, config.yaml `updates: {check: false}` turns it off,
// BV_NO_UPDATE_CHECK=1 turns it off even when config says on, and
// BV_NO_UPDATE_CHECK=0 is not an opt-out.
func TestUpdater_OptOutEnvAndConfig(t *testing.T) {
	configDir := isolateUpdaterConfig(t)

	if !StartupCheckEnabled() {
		t.Fatal("default: startup check should be enabled with no env and no config")
	}
	if LoadPreferences().UseAmbientToken {
		t.Fatal("default: ambient token must not be used")
	}

	writeUpdaterConfig(t, configDir, "theme: dark\nupdates:\n  check: false\n")
	if StartupCheckEnabled() {
		t.Fatal("config updates.check=false should disable the startup check")
	}

	writeUpdaterConfig(t, configDir, "updates:\n  check: true\n  use_token: true\n")
	if !StartupCheckEnabled() {
		t.Fatal("config updates.check=true should enable the startup check")
	}
	if !LoadPreferences().UseAmbientToken {
		t.Fatal("config updates.use_token=true should allow the ambient token")
	}

	t.Setenv(EnvNoUpdateCheck, "1")
	if StartupCheckEnabled() {
		t.Fatal("BV_NO_UPDATE_CHECK=1 should override config updates.check=true")
	}
	t.Setenv(EnvNoUpdateCheck, "0")
	if !StartupCheckEnabled() {
		t.Fatal("BV_NO_UPDATE_CHECK=0 must not count as an opt-out")
	}

	// BV_NO_SAVED_CONFIG makes bv ignore config.yaml entirely.
	writeUpdaterConfig(t, configDir, "updates:\n  check: false\n  use_token: true\n")
	t.Setenv(envNoSavedConfig, "1")
	prefs := LoadPreferences()
	if !prefs.CheckOnStartup || prefs.UseAmbientToken {
		t.Fatalf("BV_NO_SAVED_CONFIG should fall back to defaults, got %+v", prefs)
	}

	// Malformed YAML falls back to defaults rather than failing closed/open oddly.
	t.Setenv(envNoSavedConfig, "")
	writeUpdaterConfig(t, configDir, "updates: [not a mapping")
	if !StartupCheckEnabled() {
		t.Fatal("unparseable config.yaml should fall back to the default (enabled)")
	}
}

// TestUpdater_NoAmbientTokenByDefault proves that with GITHUB_TOKEN present
// in the environment the request that leaves the process carries no
// Authorization header unless the user opted in.
func TestUpdater_NoAmbientTokenByDefault(t *testing.T) {
	isolateUpdaterConfig(t)
	t.Setenv("GITHUB_TOKEN", "ghp_ambient_secret")

	server := olderReleaseServer(t)
	var seen http.Header
	client := rewriteAPIToServer(t, server.URL, &seen)

	tag, _, err := checkForUpdates(client, baseURL+"/releases/latest")
	if err != nil {
		t.Fatalf("checkForUpdates: %v", err)
	}
	if tag != "" {
		t.Fatalf("expected no newer release, got tag %q", tag)
	}
	if seen == nil {
		t.Fatal("request never reached the transport")
	}
	if got := seen.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header sent without opt-in: %q", got)
	}
	if got := seen.Get("User-Agent"); got != "beads-viewer-update-check" {
		t.Fatalf("User-Agent = %q, want beads-viewer-update-check", got)
	}
}

// TestUpdater_TokenWhenOptedIn is the positive control for
// TestUpdater_NoAmbientTokenByDefault: the same request carries the Bearer
// token once BV_UPDATE_USE_TOKEN=1 or config.yaml use_token is set.
func TestUpdater_TokenWhenOptedIn(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		isolateUpdaterConfig(t)
		t.Setenv("GITHUB_TOKEN", "ghp_ambient_secret")
		t.Setenv(EnvUseToken, "1")

		server := olderReleaseServer(t)
		var seen http.Header
		client := rewriteAPIToServer(t, server.URL, &seen)
		if _, _, err := checkForUpdates(client, baseURL+"/releases/latest"); err != nil {
			t.Fatalf("checkForUpdates: %v", err)
		}
		if got := seen.Get("Authorization"); got != "Bearer ghp_ambient_secret" {
			t.Fatalf("Authorization = %q, want Bearer ghp_ambient_secret", got)
		}
	})

	t.Run("config", func(t *testing.T) {
		configDir := isolateUpdaterConfig(t)
		t.Setenv("GH_TOKEN", "gho_from_gh_cli")
		writeUpdaterConfig(t, configDir, "updates:\n  use_token: true\n")

		server := olderReleaseServer(t)
		var seen http.Header
		client := rewriteAPIToServer(t, server.URL, &seen)
		if _, _, err := checkForUpdates(client, baseURL+"/releases/latest"); err != nil {
			t.Fatalf("checkForUpdates: %v", err)
		}
		if got := seen.Get("Authorization"); got != "Bearer gho_from_gh_cli" {
			t.Fatalf("Authorization = %q, want Bearer gho_from_gh_cli", got)
		}
	})
}

// TestUpdater_ExplicitCheckIgnoresOptOut guards the --check-update / --update
// path: BV_NO_UPDATE_CHECK only silences the TUI's automatic check, the
// explicit request still goes out.
func TestUpdater_ExplicitCheckIgnoresOptOut(t *testing.T) {
	isolateUpdaterConfig(t)
	t.Setenv(EnvNoUpdateCheck, "1")

	server := olderReleaseServer(t)
	var seen http.Header
	client := rewriteAPIToServer(t, server.URL, &seen)
	if _, _, err := checkForUpdates(client, baseURL+"/releases/latest"); err != nil {
		t.Fatalf("checkForUpdates: %v", err)
	}
	if seen == nil {
		t.Fatal("explicit check must still reach the network with BV_NO_UPDATE_CHECK=1")
	}
	if StartupCheckEnabled() {
		t.Fatal("startup check should report disabled while the explicit check still ran")
	}
}

// TestUpdater_StartupDisclosureRecordedOnce verifies the one-time footer
// disclosure is pending until recorded, and never recorded (or pending)
// under BV_NO_SAVED_CONFIG.
func TestUpdater_StartupDisclosureRecordedOnce(t *testing.T) {
	configDir := isolateUpdaterConfig(t)

	if !StartupDisclosurePending() {
		t.Fatal("fresh config dir: disclosure should be pending")
	}
	if err := RecordStartupDisclosure(); err != nil {
		t.Fatalf("RecordStartupDisclosure: %v", err)
	}
	if StartupDisclosurePending() {
		t.Fatal("disclosure should not be pending after it was recorded")
	}
	if _, err := os.Stat(filepath.Join(configDir, startupDisclosureFileName)); err != nil {
		t.Fatalf("marker file missing: %v", err)
	}

	// Planted negative: with saved config disabled nothing is written and
	// nothing is reported as pending, even in a fresh directory.
	fresh := isolateUpdaterConfig(t)
	t.Setenv(envNoSavedConfig, "1")
	if StartupDisclosurePending() {
		t.Fatal("BV_NO_SAVED_CONFIG: disclosure must not be pending")
	}
	if err := RecordStartupDisclosure(); err != nil {
		t.Fatalf("RecordStartupDisclosure under BV_NO_SAVED_CONFIG: %v", err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("BV_NO_SAVED_CONFIG: config dir must not be created, stat err=%v", err)
	}
}

// TestUpdater_MatchesVersionedAssetNames (#195): releases now ship
// bv_<version>_<os>_<arch>.<ext>; the updater must pick that name, still
// accept the unversioned name older releases used, and prefer the versioned
// one when a release carries both.
func TestUpdater_MatchesVersionedAssetNames(t *testing.T) {
	versioned := getAssetName("v1.2.3")
	legacy := stableAssetName()
	if versioned == legacy || !strings.Contains(versioned, "1.2.3") {
		t.Fatalf("versioned name %q should embed the version and differ from legacy %q", versioned, legacy)
	}

	rel := &Release{TagName: "v1.2.3", Assets: []Asset{{Name: "checksums.txt"}, {Name: versioned}}}
	if got := rel.FindPlatformAsset(); got == nil || got.Name != versioned {
		t.Fatalf("versioned-only release: got %+v, want %s", got, versioned)
	}

	rel = &Release{TagName: "v1.2.3", Assets: []Asset{{Name: legacy}}}
	if got := rel.FindPlatformAsset(); got == nil || got.Name != legacy {
		t.Fatalf("legacy-only release: got %+v, want %s", got, legacy)
	}

	rel = &Release{TagName: "v1.2.3", Assets: []Asset{{Name: legacy}, {Name: versioned}}}
	if got := rel.FindPlatformAsset(); got == nil || got.Name != versioned {
		t.Fatalf("both present: got %+v, want the versioned %s", got, versioned)
	}

	if names := platformAssetNames("v1.2.3"); names[0] != versioned || names[1] != legacy {
		t.Fatalf("lookup order should be versioned then legacy, got %v", names)
	}
	if names := platformAssetNames(""); len(names) != 1 || names[0] != legacy {
		t.Fatalf("without a tag only the legacy name applies, got %v", names)
	}
}

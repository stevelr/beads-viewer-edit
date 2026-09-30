package export

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Prevent any test from accidentally opening a browser
	os.Setenv("BV_NO_BROWSER", "1")
	os.Setenv("BV_TEST_MODE", "1")

	// Cloudflare deploy tests rely on simulating an unauthenticated wrangler
	// via stub binaries. Real CLOUDFLARE_API_TOKEN/ACCOUNT_ID values inherited
	// from the developer's shell short-circuit CheckWranglerStatus to
	// Authenticated=true and bypass the unauth path under test, so unset them
	// before any test runs (bv-142 follow-up).
	os.Unsetenv("CLOUDFLARE_API_TOKEN")
	os.Unsetenv("CLOUDFLARE_ACCOUNT_ID")
	// Tests override HOME to hide the real wrangler OAuth config, but
	// checkWranglerConfigFile also reads $XDG_CONFIG_HOME/.wrangler.
	os.Unsetenv("XDG_CONFIG_HOME")

	os.Exit(m.Run())
}

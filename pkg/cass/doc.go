// Package cass provides integration with Coding Agent Session Search.
//
// Cass (https://github.com/Dicklesworthstone/coding_agent_session_search) searches
// archived coding-agent conversations. This package detects cass and adapts its
// bounded JSON search responses into session previews and correlation inputs.
//
// # Detection Flow
//
// The Detector performs a two-step detection process:
//
//  1. Check if "cass" exists in PATH using exec.LookPath
//  2. Run "cass health" with a configurable timeout (default 2s)
//
// # Health Status
//
// Based on the exit code from "cass health":
//
//	Exit 0: StatusHealthy - ready to search
//	Exit 1: StatusNeedsIndex - archive health needs attention; try bounded search
//	Exit 3: StatusNeedsIndex - index missing or corrupt
//	Other:  StatusNotInstalled - treat as unavailable
//
// A stale or rebuilding archive can remain searchable. Searches may run in
// StatusNeedsIndex without changing that cached health status to Healthy.
//
// # Caching
//
// Detection results are cached for 5 minutes by default (configurable).
// This avoids repeated subprocess calls during normal operation.
// The cache can be invalidated explicitly via Invalidate() or will
// automatically expire after the TTL.
//
// # Thread Safety
//
// The Detector is safe for concurrent use. All exported methods use
// appropriate locking to prevent data races.
//
// # Example Usage
//
//	detector := cass.NewDetector()
//	if detector.Check() == cass.StatusHealthy {
//	    // Safe to use cass for searching
//	    results := runCassSearch(query)
//	}
//
// # Silent Failure
//
// This package is designed for silent degradation. When cass is not available,
// it returns StatusNotInstalled without logging errors or warnings. The
// consuming code should simply disable cass-dependent features.
package cass

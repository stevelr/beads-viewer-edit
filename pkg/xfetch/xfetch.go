package xfetch

import (
	"math"
	"math/rand"
	"time"
)

// XFetch implements probabilistic early refresh to prevent cache stampedes.
// Based on the "XFetch" algorithm from "Optimal Probabilistic Cache Stampede Prevention"
// (Vattani, Chierichetti, Lowenstein 2015).
//
// The algorithm probabilistically decides whether to refresh a cached value
// before its expiry, with refresh probability increasing as expiry approaches.
// This spreads refresh load over time instead of all clients refreshing simultaneously.

// ShouldRefresh returns true if a cached value should be refreshed early.
//
// Parameters:
//   - expiresAt: when the cached value expires (creation time plus its TTL)
//   - computeDuration: how long the last computation took (used for gap estimation)
//   - beta: scaling factor (1.0 is standard; higher = more aggressive early refresh)
//   - now: current time
//
// Refresh probability increases as now approaches expiresAt. The computation
// duration sets the early-refresh window, not the lifetime of the cache entry.
func ShouldRefresh(expiresAt time.Time, computeDuration time.Duration, beta float64, now time.Time) bool {
	// Float64 returns [0,1); reflection gives (0,1] without clamping log(0)
	// to an arbitrary tail cutoff.
	return shouldRefresh(expiresAt, computeDuration, beta, now, 1-rand.Float64())
}

// shouldRefresh accepts an explicit uniform draw so the expiry policy can be
// checked against fixed quantiles without random or wall-clock test failures.
func shouldRefresh(expiresAt time.Time, computeDuration time.Duration, beta float64, now time.Time, uniform float64) bool {
	if computeDuration <= 0 || beta <= 0 || math.IsNaN(beta) || math.IsInf(beta, 0) {
		return false
	}
	if !now.Before(expiresAt) {
		return true
	}
	if uniform == 1 {
		return false // A zero gap cannot reach a future expiry, even for huge beta.
	}

	// Figure 3: now - duration * beta * log(uniform) >= expiry.
	// Compare the remaining lifetime with the sampled gap in floating point;
	// converting a large gap to time.Duration could overflow and reverse it.
	// Normalize first so even an overflowing duration*beta and log(1)==0
	// cannot produce Inf*0 (NaN).
	remaining := float64(expiresAt.Sub(now)) / float64(computeDuration) / beta
	return remaining <= -math.Log(uniform)
}

// ShouldRefreshWithDefault is a convenience wrapper using time.Now() and beta=1.0.
func ShouldRefreshWithDefault(expiresAt time.Time, computeDuration time.Duration) bool {
	return ShouldRefresh(expiresAt, computeDuration, 1.0, time.Now())
}

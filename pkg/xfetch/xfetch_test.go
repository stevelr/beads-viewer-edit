package xfetch

import (
	"math"
	"testing"
	"time"
)

func TestXFetchShouldRefresh_AtExpiry(t *testing.T) {
	expiresAt := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	if !ShouldRefresh(expiresAt, time.Second, 1, expiresAt) {
		t.Fatal("an entry at its expiry must refresh")
	}
}

func TestXFetchShouldRefresh_ZeroDuration(t *testing.T) {
	now := time.Now()
	lastCompute := now.Add(-time.Second)

	// Zero duration should never trigger refresh
	if ShouldRefresh(lastCompute, 0, 1.0, now) {
		t.Error("expected no refresh with zero duration")
	}

	// Negative duration should never trigger refresh
	if ShouldRefresh(lastCompute, -time.Second, 1.0, now) {
		t.Error("expected no refresh with negative duration")
	}
}

func TestXFetchShouldRefresh_InvalidBeta(t *testing.T) {
	now := time.Now()
	lastCompute := now.Add(-time.Hour)
	computeDuration := time.Minute

	tests := []struct {
		name string
		beta float64
	}{
		{name: "zero", beta: 0},
		{name: "negative", beta: -1},
		{name: "nan", beta: math.NaN()},
		{name: "positive inf", beta: math.Inf(1)},
		{name: "negative inf", beta: math.Inf(-1)},
	}

	for _, tt := range tests {
		if ShouldRefresh(lastCompute, computeDuration, tt.beta, now) {
			t.Fatalf("expected no refresh for invalid beta %s", tt.name)
		}
	}
}

func TestXFetchShouldRefresh_Deterministic(t *testing.T) {
	// An expired entry always refreshes, independently of the random draw.
	expiresAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	computeDuration := time.Hour
	farFuture := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 100; i++ {
		if !ShouldRefresh(expiresAt, computeDuration, 1.0, farFuture) {
			t.Fatal("expired entry did not refresh")
		}
	}
}

func TestXFetchShouldRefresh_NeverRefreshImmediately(t *testing.T) {
	// With one day remaining and a 1ns compute duration, none of the random
	// draws representable by Float64 can bridge the gap to expiry.
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	computeDuration := time.Nanosecond

	// Run multiple times
	for i := 0; i < 100; i++ {
		if ShouldRefresh(expiresAt, computeDuration, 1.0, now) {
			t.Fatal("fresh entry refreshed despite its distant expiry")
		}
	}
}

func TestXFetchShouldRefresh_ProbabilisticDistribution(t *testing.T) {
	// One computation-duration before expiry, the exponential survival
	// probability is 1/e. Uniform midpoint quantiles make this reproducible.
	computeDuration := time.Hour
	expiresAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	testTime := expiresAt.Add(-computeDuration)

	const samples = 1000
	refreshCount := 0
	for i := 0; i < samples; i++ {
		uniform := (float64(i) + 0.5) / samples
		if shouldRefresh(expiresAt, computeDuration, 1.0, testTime, uniform) {
			refreshCount++
		}
	}

	if refreshCount != 368 {
		t.Errorf("one duration before expiry: expected 368/1000 quantiles (1/e), got %d", refreshCount)
	}
}

func TestXFetchShouldRefresh_BetaScaling(t *testing.T) {
	// Higher beta stretches the early-refresh window toward earlier times.
	computeDuration := time.Hour
	expiresAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	testTime := expiresAt.Add(-computeDuration)

	const samples = 500

	// Beta = 0.5: survival probability exp(-2).
	refreshLowBeta := 0
	for i := 0; i < samples; i++ {
		if shouldRefresh(expiresAt, computeDuration, 0.5, testTime, (float64(i)+0.5)/samples) {
			refreshLowBeta++
		}
	}

	// Beta = 2.0: survival probability exp(-0.5).
	refreshHighBeta := 0
	for i := 0; i < samples; i++ {
		if shouldRefresh(expiresAt, computeDuration, 2.0, testTime, (float64(i)+0.5)/samples) {
			refreshHighBeta++
		}
	}

	if refreshLowBeta != 68 || refreshHighBeta != 303 {
		t.Errorf("expected beta=0.5 to refresh 68/500 and beta=2 to refresh 303/500: got %d and %d",
			refreshLowBeta, refreshHighBeta)
	}
}

func TestShouldRefreshWithDefault(t *testing.T) {
	oldTime := time.Now().Add(-365 * 24 * time.Hour)
	computeDuration := time.Minute
	for i := 0; i < 10; i++ {
		if !ShouldRefreshWithDefault(oldTime, computeDuration) {
			t.Fatal("expired cache did not refresh through the default wrapper")
		}
	}
}

func TestXFetchShouldRefresh_Boundaries(t *testing.T) {
	expiry := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		before   time.Duration
		duration time.Duration
		beta     float64
		uniform  float64
		want     bool
	}{
		{"at expiry with zero gap", 0, time.Second, 1, 1, true},
		{"before expiry with zero gap", time.Nanosecond, time.Second, 1, 1, false},
		{"after expiry", -time.Nanosecond, time.Second, 1, 1, true},
		{"before early window", 2 * time.Second, time.Second, 1, 0.5, false},
		{"inside early window", time.Second / 2, time.Second, 1, 0.5, true},
		{"duration stretches window", 2 * time.Second, 4 * time.Second, 1, 0.5, true},
		{"huge scale", time.Hour, time.Duration(math.MaxInt64), math.MaxFloat64, 0.5, true},
		{"huge scale zero gap", time.Nanosecond, time.Duration(math.MaxInt64), math.MaxFloat64, 1, false},
		{"tiny scale", time.Nanosecond, time.Nanosecond, math.SmallestNonzeroFloat64, 0.5, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRefresh(expiry, tt.duration, tt.beta, expiry.Add(-tt.before), tt.uniform); got != tt.want {
				t.Errorf("refresh=%v, want %v", got, tt.want)
			}
		})
	}
}

package model

import (
	"encoding/json"
	"testing"
	"time"
)

// Issue #191: defer_until must decode from beads JSONL (including a non-UTC
// offset), deep-copy on Clone, and gate readiness strictly on "still in the
// future".
func TestIssue_DeferUntilDecodeCloneAndIsDeferredAt(t *testing.T) {
	raw := `{"id":"D-1","title":"Later","status":"open","issue_type":"task","defer_until":"2027-02-03T09:30:00-05:00"}`
	var issue Issue
	if err := json.Unmarshal([]byte(raw), &issue); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := time.Date(2027, 2, 3, 14, 30, 0, 0, time.UTC) // 09:30-05:00
	if issue.DeferUntil == nil || !issue.DeferUntil.Equal(want) {
		t.Fatalf("defer_until = %v, want %v", issue.DeferUntil, want)
	}

	if !issue.IsDeferredAt(want.Add(-time.Nanosecond)) {
		t.Fatal("an instant before defer_until must still be deferred")
	}
	if issue.IsDeferredAt(want) {
		t.Fatal("defer_until reached exactly must no longer be deferred")
	}
	if issue.IsDeferredAt(want.Add(time.Hour)) {
		t.Fatal("an instant after defer_until must not be deferred")
	}
	// Same instant expressed in another zone: instant comparison, not wall time.
	if issue.IsDeferredAt(want.In(time.FixedZone("X", -10*3600))) {
		t.Fatal("zone of the reference instant must not affect the verdict")
	}

	clone := issue.Clone()
	if clone.DeferUntil == nil || clone.DeferUntil == issue.DeferUntil {
		t.Fatal("Clone must deep-copy defer_until")
	}
	if !clone.DeferUntil.Equal(*issue.DeferUntil) {
		t.Fatalf("clone defer_until = %v, want %v", clone.DeferUntil, issue.DeferUntil)
	}

	// Round-trip keeps the field (and omits it when unset).
	out, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Issue
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if back.DeferUntil == nil || !back.DeferUntil.Equal(want) {
		t.Fatalf("round-trip defer_until = %v, want %v", back.DeferUntil, want)
	}
}

func TestIssue_IsDeferredAtWithoutDeferUntil(t *testing.T) {
	issue := Issue{ID: "N-1", Title: "No deferral", Status: StatusOpen}
	if issue.IsDeferredAt(time.Now()) {
		t.Fatal("issue without defer_until must never be deferred")
	}
	if issue.IsDeferredAt(time.Time{}) {
		t.Fatal("issue without defer_until must never be deferred, even at the zero instant")
	}
	out, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) == "" || json.Valid(out) && containsKey(out, "defer_until") {
		t.Fatalf("unset defer_until must be omitted from JSON, got %s", out)
	}
}

func containsKey(doc []byte, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(doc, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

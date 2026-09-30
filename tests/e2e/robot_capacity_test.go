package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRobotCapacity_DenseDAG(t *testing.T) {
	bv := buildBvBinary(t)
	if control := os.Getenv("BV_CAPACITY_TEST_BINARY"); control != "" {
		bv = control
	}
	t.Setenv("SOURCE_DATE_EPOCH", "1788912000")
	dir := t.TempDir()
	var lines []string
	var wantPath []string
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("n%02d", i)
		var deps []map[string]string
		for j := 0; j < i; j++ {
			deps = append(deps, map[string]string{"depends_on_id": wantPath[j], "type": "blocks"})
		}
		row, err := json.Marshal(map[string]any{"id": id, "title": id, "status": "open", "issue_type": "task", "priority": 1, "estimated_minutes": 60, "dependencies": deps})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(row))
		wantPath = append(wantPath, id)
	}
	writeIssuesJSONL(t, dir, strings.Join(lines, "\n")+"\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bv, "--robot-capacity", "--agents=3")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	t.Logf("binary=%q nodes=64 edges=2016 elapsed=%s exit=%v stderr=%s stdout=%s", bv, time.Since(start), err, stderr.String(), out)
	if err != nil {
		t.Fatalf("dense acyclic capacity must complete within five seconds: %v context=%v", err, ctx.Err())
	}
	var got struct {
		Path       []string `json:"critical_path"`
		Length     int      `json:"critical_path_length"`
		Open       int      `json:"open_issue_count"`
		Actionable []string `json:"actionable"`
		Total      int      `json:"total_minutes"`
		Serial     int      `json:"serial_minutes"`
		Parallel   int      `json:"parallel_minutes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Path, wantPath) || got.Length != 64 || got.Open != 64 || !slices.Equal(got.Actionable, wantPath[:1]) {
		t.Fatalf("wrong complete serial path/readiness: %+v", got)
	}
	if got.Total <= 0 || got.Serial != got.Total || got.Parallel != 0 {
		t.Fatalf("complete chain must retain entirely serial work: %+v", got)
	}
}

func TestRobotCapacity_ReadinessScope(t *testing.T) {
	bv := buildBvBinary(t)
	if control := os.Getenv("BV_CAPACITY_TEST_BINARY"); control != "" {
		bv = control
	}
	t.Setenv("SOURCE_DATE_EPOCH", "1788912000")
	dir := t.TempDir()
	writeIssuesJSONL(t, dir, strings.Join([]string{
		`{"id":"outer","title":"Outside blocker","status":"open","issue_type":"task","priority":1,"labels":["other"]}`,
		`{"id":"related","title":"Related work","status":"open","issue_type":"task","priority":1,"labels":["focus"],"dependencies":[{"depends_on_id":"outer","type":"related"}]}`,
		`{"id":"missing","title":"Missing gate","status":"open","issue_type":"task","priority":1,"labels":["focus"],"dependencies":[{"depends_on_id":"absent","type":"blocks"}]}`,
		`{"id":"deferred","title":"Future work","status":"open","issue_type":"task","priority":1,"labels":["focus"],"defer_until":"2099-01-01T00:00:00Z"}`,
		`{"id":"parked","title":"Parked work","status":"blocked","issue_type":"task","priority":1,"labels":["focus"]}`,
		`{"id":"parent","title":"Blocked parent","status":"open","issue_type":"task","priority":1,"labels":["focus"],"dependencies":[{"depends_on_id":"outer","type":"blocks"}]}`,
		`{"id":"child","title":"Inherited gate","status":"open","issue_type":"task","priority":1,"labels":["focus"],"dependencies":[{"depends_on_id":"parent","type":"parent-child"}]}`,
		`{"id":"resolved","title":"Resolved gates","status":"open","issue_type":"task","priority":1,"labels":["focus"],"dependencies":[{"depends_on_id":"done","type":"blocks"},{"depends_on_id":"gone","type":"conditional-blocks"}]}`,
		`{"id":"done","title":"Done","status":"closed","issue_type":"task","priority":1,"labels":["other"]}`,
		`{"id":"gone","title":"Deleted","status":"tombstone","issue_type":"task","priority":1,"labels":["other"]}`,
	}, "\n")+"\n")
	for _, tc := range []struct {
		name      string
		flags     []string
		planFlags []string
		want      []string
		open      int
	}{
		{"unscoped", nil, nil, []string{"outer", "related", "resolved"}, 8},
		{"capacity_label", []string{"--capacity-label", "focus"}, []string{"--label", "focus"}, []string{"related", "resolved"}, 7},
		{"global_label", []string{"--label", "focus"}, []string{"--label", "focus"}, []string{"related", "resolved"}, 7},
		{"matching_intersection", []string{"--label", "focus", "--capacity-label", "focus"}, []string{"--label", "focus"}, []string{"related", "resolved"}, 7},
		{"disjoint_intersection", []string{"--label", "focus", "--capacity-label", "other"}, []string{"--label", "absent"}, nil, 0},
		{"recipe", []string{"--label", "focus", "--recipe", "actionable"}, []string{"--label", "focus", "--recipe", "actionable"}, []string{"related", "resolved"}, 2},
		{"empty", []string{"--label", "absent"}, []string{"--label", "absent"}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--robot-capacity"}, tc.flags...)
			r := runScoped(t, bv, dir, args...)
			t.Logf("binary=%q argv=%q exit=%v stderr=%s stdout=%s", bv, args, r.exit, r.stderr, r.stdout)
			if r.exit != nil {
				t.Fatal(r.exit)
			}
			var got struct {
				Actionable []string `json:"actionable"`
				Count      int      `json:"actionable_count"`
				Open       int      `json:"open_issue_count"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Actionable, tc.want) || got.Count != len(tc.want) || got.Open != tc.open {
				t.Errorf("capacity=%+v, want ready=%v open=%d", got, tc.want, tc.open)
			}
			plan := runScoped(t, bv, dir, append([]string{"--robot-plan"}, tc.planFlags...)...)
			var canonical struct {
				Plan struct {
					Total  int `json:"total_actionable"`
					Tracks []struct {
						Items []struct{ ID string } `json:"items"`
					} `json:"tracks"`
				} `json:"plan"`
			}
			if plan.exit != nil || json.Unmarshal([]byte(plan.stdout), &canonical) != nil {
				t.Fatalf("plan failed: %v stdout=%s stderr=%s", plan.exit, plan.stdout, plan.stderr)
			}
			var ready []string
			for _, track := range canonical.Plan.Tracks {
				for _, item := range track.Items {
					ready = append(ready, item.ID)
				}
			}
			slices.Sort(ready)
			if !slices.Equal(got.Actionable, ready) || got.Count != canonical.Plan.Total {
				t.Errorf("capacity readiness differs from plan: capacity=%+v plan=%s", got, plan.stdout)
			}
		})
	}
}

func TestRobotCapacity_EstimatedDaysDropsWithMoreAgents(t *testing.T) {
	bv := buildBvBinary(t)
	env := t.TempDir()

	now := time.Now().UTC().Format(time.RFC3339)

	// Three independent tasks; estimated_minutes drives total_minutes deterministically.
	writeBeads(t, env, fmt.Sprintf(
		`{"id":"A","title":"A","status":"open","priority":1,"issue_type":"task","estimated_minutes":480,"labels":["backend"],"created_at":"%s","updated_at":"%s"}
{"id":"B","title":"B","status":"open","priority":1,"issue_type":"task","estimated_minutes":480,"labels":["backend"],"created_at":"%s","updated_at":"%s"}
{"id":"C","title":"C","status":"open","priority":1,"issue_type":"task","estimated_minutes":480,"labels":["frontend"],"created_at":"%s","updated_at":"%s"}`,
		now, now, now, now, now, now,
	))

	run := func(args ...string) struct {
		Agents         int     `json:"agents"`
		Label          string  `json:"label"`
		OpenIssueCount int     `json:"open_issue_count"`
		EstimatedDays  float64 `json:"estimated_days"`
		TotalMinutes   int     `json:"total_minutes"`
	} {
		t.Helper()
		cmd := exec.Command(bv, args...)
		cmd.Dir = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
		var payload struct {
			Agents         int     `json:"agents"`
			Label          string  `json:"label"`
			OpenIssueCount int     `json:"open_issue_count"`
			EstimatedDays  float64 `json:"estimated_days"`
			TotalMinutes   int     `json:"total_minutes"`
		}
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("json decode: %v\nout=%s", err, out)
		}
		return payload
	}

	one := run("--robot-capacity", "--agents=1")
	three := run("--robot-capacity", "--agents=3")

	if one.OpenIssueCount != 3 || three.OpenIssueCount != 3 {
		t.Fatalf("open_issue_count mismatch: one=%d three=%d", one.OpenIssueCount, three.OpenIssueCount)
	}
	if one.TotalMinutes <= 0 || three.TotalMinutes != one.TotalMinutes {
		t.Fatalf("total_minutes mismatch: one=%d three=%d", one.TotalMinutes, three.TotalMinutes)
	}
	if !(three.EstimatedDays < one.EstimatedDays) {
		t.Fatalf("expected estimated_days to drop with more agents: one=%.3f three=%.3f", one.EstimatedDays, three.EstimatedDays)
	}

	// Label filter.
	backend := run("--robot-capacity", "--capacity-label=backend", "--agents=1")
	if backend.Label != "backend" {
		t.Fatalf("label=%q; want backend", backend.Label)
	}
	if backend.OpenIssueCount != 2 {
		t.Fatalf("backend open_issue_count=%d; want 2", backend.OpenIssueCount)
	}
}

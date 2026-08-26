package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/loader"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	"github.com/Dicklesworthstone/beads_viewer/pkg/testutil"
	json "github.com/goccy/go-json"
)

func copyIssues(in []model.Issue) []model.Issue {
	if in == nil {
		return nil
	}
	out := make([]model.Issue, len(in))
	copy(out, in)
	return out
}

func BenchmarkSnapshotSwap(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("issues=%d", size), func(b *testing.B) {
			issues := testutil.QuickRandom(size, 0.01)
			modifiedIssues := copyIssues(issues)
			modifiedID := modifiedIssues[len(modifiedIssues)/2].ID
			modifiedIssues[len(modifiedIssues)/2].Title += " updated"

			m := NewModel(copyIssues(issues), nil, "")
			snapshots := [2]*DataSnapshot{
				NewSnapshotBuilder(copyIssues(issues)).Build(),
				NewSnapshotBuilder(modifiedIssues).Build(),
			}
			for _, snapshot := range snapshots {
				snapshot.IssueDiff = &analysis.IssueDiff{Modified: []string{modifiedID}}
			}

			tm, _ := m.Update(SnapshotReadyMsg{Snapshot: snapshots[0]})
			m = tm.(*Model)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tm, _ := m.Update(SnapshotReadyMsg{Snapshot: snapshots[i&1]})
				m = tm.(*Model)
			}
		})
	}
}

func BenchmarkDuplicateSnapshotDelivery(b *testing.B) {
	issues := testutil.QuickRandom(1000, 0.01)
	m := NewModel(copyIssues(issues), nil, "")
	snapshot := NewSnapshotBuilder(copyIssues(issues)).Build()
	tm, _ := m.Update(SnapshotReadyMsg{Snapshot: snapshot})
	m = tm.(*Model)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tm, _ = m.Update(SnapshotReadyMsg{Snapshot: snapshot})
		m = tm.(*Model)
	}
}

func BenchmarkSnapshotViewSyncComponents(b *testing.B) {
	issues := testutil.QuickRandom(1000, 0.01)
	m := NewModel(copyIssues(issues), nil, "")
	snapshot := NewSnapshotBuilder(copyIssues(issues)).Build()

	b.Run("list", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.installSnapshotListItems(snapshot)
		}
	})
	b.Run("board", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.board.SetSnapshot(snapshot)
		}
	})
	b.Run("graph", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.graphView.SetSnapshot(snapshot)
		}
	})
	b.Run("insights", func(b *testing.B) {
		insights := snapshot.GetInsights()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.insightsPanel.SetInsights(insights)
		}
	})
}

func BenchmarkKeyPressLatency(b *testing.B) {
	issues := testutil.QuickRandom(1000, 0.01)
	m := NewModel(issues, nil, "")
	durations := make([]time.Duration, 0, b.N)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(*Model)
		if view := m.View(); view == "" {
			b.Fatal("View returned empty output")
		}
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99Index := (99*len(durations)+99)/100 - 1
	b.ReportMetric(float64(durations[p99Index].Nanoseconds()), "p99-ns/op")
}

func BenchmarkKeyPressUpdateLatency(b *testing.B) {
	issues := testutil.QuickRandom(1000, 0.01)
	m := NewModel(issues, nil, "")
	durations := make([]time.Duration, 0, b.N)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(*Model)
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99Index := (99*len(durations)+99)/100 - 1
	b.ReportMetric(float64(durations[p99Index].Nanoseconds()), "p99-ns/op")
}

func BenchmarkSnapshotBuilderBuild(b *testing.B) {
	cfg := analysis.AnalysisConfig{}

	for _, size := range []int{100, 500, 1000, 5000} {
		b.Run(fmt.Sprintf("issues=%d", size), func(b *testing.B) {
			base := testutil.QuickRandom(size, 0.01)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				issues := copyIssues(base)
				b.StartTimer()

				builder := NewSnapshotBuilder(issues)
				stats := builder.analyzer.AnalyzeWithConfig(cfg)
				builder.WithAnalysis(&stats)

				snap := builder.Build()
				if snap == nil {
					b.Fatalf("unexpected snapshot: nil")
				}
				if len(snap.Issues) != len(base) {
					b.Fatalf("unexpected snapshot issue count: got=%d want=%d", len(snap.Issues), len(base))
				}
			}
		})
	}
}

func BenchmarkListItemBuild(b *testing.B) {
	issues := testutil.QuickRandom(1000, 0.01)
	prev := NewSnapshotBuilder(copyIssues(issues)).Build()
	updated := copyIssues(prev.Issues)
	updated[len(updated)/2].Title += " updated"
	diff := analysis.ComputeIssueDiff(prev.Issues, updated)

	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			items := buildListItems(updated, nil)
			if len(items) != len(updated) {
				b.Fatal("incomplete full list build")
			}
		}
	})
	b.Run("one-of-1000-incremental", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			items := buildListItemsIncremental(updated, nil, prev, &diff)
			if len(items) != len(updated) {
				b.Fatal("incomplete incremental list build")
			}
		}
	})
}

func BenchmarkBackgroundWorkerBuildSnapshot(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run(fmt.Sprintf("issues=%d", size), func(b *testing.B) {
			issues := testutil.QuickRandom(size, 0.01)
			beadsPath := writeBenchmarkIssues(b, issues)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				worker, err := NewBackgroundWorker(WorkerConfig{
					BeadsPath: beadsPath,
					IdleGC:    &IdleGCConfig{Enabled: false},
				})
				if err != nil {
					b.Fatalf("new background worker: %v", err)
				}
				b.StartTimer()

				snapshot := worker.buildSnapshot(true)

				b.StopTimer()
				if snapshot == nil {
					b.Fatal("buildSnapshot returned nil")
				}
				if got := len(snapshot.Issues); got != size {
					b.Fatalf("snapshot issue count=%d, want %d", got, size)
				}
				if snapshot.Analysis != nil {
					snapshot.Analysis.WaitForPhase2()
				}
				worker.cancel()
				loader.ReturnIssuePtrsToPool(snapshot.pooledIssues)
				b.StartTimer()
			}
		})
	}
}

func writeBenchmarkIssues(b *testing.B, issues []model.Issue) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "issues.jsonl")
	file, err := os.Create(path)
	if err != nil {
		b.Fatalf("create benchmark issues: %v", err)
	}
	w := bufio.NewWriter(file)
	for i := range issues {
		line, err := json.Marshal(issues[i])
		if err != nil {
			_ = file.Close()
			b.Fatalf("marshal benchmark issue: %v", err)
		}
		if _, err := w.Write(line); err != nil {
			_ = file.Close()
			b.Fatalf("write benchmark issue: %v", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			_ = file.Close()
			b.Fatalf("terminate benchmark issue: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = file.Close()
		b.Fatalf("flush benchmark issues: %v", err)
	}
	if err := file.Close(); err != nil {
		b.Fatalf("close benchmark issues: %v", err)
	}
	return path
}

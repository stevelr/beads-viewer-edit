package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Dicklesworthstone/beads_viewer/pkg/correlation"
)

func TestRobotRegistryValidate_RejectsModifierAlone(t *testing.T) {
	var robotTriage bool
	robotByLabel := "backend"

	registry := newRobotRegistry()
	registry.Register(RobotCommand{
		Name:        "robot-triage",
		FlagName:    "robot-triage",
		FlagPtr:     &robotTriage,
		Description: "Unified triage output",
	})
	registry.Register(RobotCommand{
		Name:            "robot-by-label",
		FlagName:        "robot-by-label",
		FlagPtr:         &robotByLabel,
		RequiredCoFlags: []string{"robot-triage", "robot-insights", "robot-plan", "robot-priority"},
		IsModifier:      true,
		Description:     "Filter robot output by label",
	})
	registry.Register(RobotCommand{
		Name:        "robot-insights",
		FlagName:    "robot-insights",
		FlagPtr:     ptrTo(false),
		Description: "Insights output",
	})
	registry.Register(RobotCommand{
		Name:        "robot-plan",
		FlagName:    "robot-plan",
		FlagPtr:     ptrTo(false),
		Description: "Plan output",
	})
	registry.Register(RobotCommand{
		Name:        "robot-priority",
		FlagName:    "robot-priority",
		FlagPtr:     ptrTo(false),
		Description: "Priority output",
	})

	err := registry.Validate()
	if err == nil {
		t.Fatal("expected modifier-alone validation error")
	}
	if !strings.Contains(err.Error(), "--robot-by-label") {
		t.Fatalf("expected error to mention modifier flag, got %q", err)
	}
	if !strings.Contains(err.Error(), "--robot-triage") {
		t.Fatalf("expected error to mention required co-flag, got %q", err)
	}

	robotTriage = true
	if err := registry.Validate(); err != nil {
		t.Fatalf("expected modifier to validate once paired with primary flag: %v", err)
	}
}

func TestRobotRegistryAnyActive_MatchesOldLogic(t *testing.T) {
	var (
		robotHelp       bool
		robotInsights   bool
		robotTriage     bool
		robotSearch     bool
		robotFileBeads  string
		robotByLabel    string
		robotByAssignee string
		robotDocs       string
	)

	registry := newRobotRegistry()
	registry.Register(RobotCommand{Name: "robot-help", FlagName: "robot-help", FlagPtr: &robotHelp, Description: "Help"})
	registry.Register(RobotCommand{Name: "robot-insights", FlagName: "robot-insights", FlagPtr: &robotInsights, Description: "Insights"})
	registry.Register(RobotCommand{Name: "robot-triage", FlagName: "robot-triage", FlagPtr: &robotTriage, Description: "Triage"})
	registry.Register(RobotCommand{Name: "robot-search", FlagName: "robot-search", FlagPtr: &robotSearch, Description: "Search"})
	registry.Register(RobotCommand{Name: "robot-file-beads", FlagName: "robot-file-beads", FlagPtr: &robotFileBeads, Description: "File beads"})
	registry.Register(RobotCommand{
		Name:            "robot-by-label",
		FlagName:        "robot-by-label",
		FlagPtr:         &robotByLabel,
		RequiredCoFlags: []string{"robot-insights", "robot-triage"},
		IsModifier:      true,
		Description:     "Label filter",
	})
	registry.Register(RobotCommand{
		Name:            "robot-by-assignee",
		FlagName:        "robot-by-assignee",
		FlagPtr:         &robotByAssignee,
		RequiredCoFlags: []string{"robot-insights", "robot-triage"},
		IsModifier:      true,
		Description:     "Assignee filter",
	})
	registry.Register(RobotCommand{Name: "robot-docs", FlagName: "robot-docs", FlagPtr: &robotDocs, Description: "Docs"})

	oldLogic := func() bool {
		return robotHelp ||
			robotInsights ||
			robotTriage ||
			robotSearch ||
			robotFileBeads != "" ||
			robotByLabel != "" ||
			robotByAssignee != "" ||
			robotDocs != ""
	}

	tests := []struct {
		name  string
		setup func()
	}{
		{name: "none active", setup: func() {}},
		{name: "help active", setup: func() { robotHelp = true }},
		{name: "primary robot command active", setup: func() { robotTriage = true }},
		{name: "string command active", setup: func() { robotFileBeads = "pkg/ui/model.go" }},
		{name: "modifier alone still enables robot mode", setup: func() { robotByLabel = "backend" }},
		{name: "docs topic active", setup: func() { robotDocs = "commands" }},
		{name: "multiple mixed flags", setup: func() {
			robotSearch = true
			robotByAssignee = "alice"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			robotHelp = false
			robotInsights = false
			robotTriage = false
			robotSearch = false
			robotFileBeads = ""
			robotByLabel = ""
			robotByAssignee = ""
			robotDocs = ""

			tt.setup()

			if got, want := registry.AnyActive(), oldLogic(); got != want {
				t.Fatalf("AnyActive()=%v, want %v", got, want)
			}
		})
	}
}

func TestRobotRegistryDispatchFlag_RunsActiveHandler(t *testing.T) {
	var robotHelp bool
	var called int

	registry := newRobotRegistry()
	registry.Register(RobotCommand{
		Name:     "robot-help",
		FlagName: "robot-help",
		FlagPtr:  &robotHelp,
		Handler: func(ctx RobotContext) error {
			called++
			if got := ctx.StdoutOrDefault(); got != ctx.Stdout {
				t.Fatalf("expected dispatch to preserve stdout writer")
			}
			return nil
		},
	})

	stdout := &bytes.Buffer{}
	ctx := RobotContext{Stdout: stdout}

	handled, err := registry.DispatchFlag("robot-help", ctx)
	if err != nil {
		t.Fatalf("inactive flag should not error: %v", err)
	}
	if handled {
		t.Fatal("inactive flag should not dispatch")
	}

	robotHelp = true
	handled, err = registry.DispatchFlag("robot-help", ctx)
	if err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	if !handled {
		t.Fatal("active flag should dispatch")
	}
	if called != 1 {
		t.Fatalf("handler call count = %d, want 1", called)
	}
}

func TestDispatchRobotFlagResult_ReturnsComposableOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var robotHelp bool

		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return nil
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if result.Handled {
			t.Fatal("inactive flag should not dispatch")
		}
		if result.ExitCode != 0 {
			t.Fatalf("inactive flag exit code = %d, want 0", result.ExitCode)
		}

		robotHelp = true
		result = dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 0 {
			t.Fatalf("successful dispatch exit code = %d, want 0", result.ExitCode)
		}
		if result.Err != nil {
			t.Fatalf("successful dispatch should not return error: %v", result.Err)
		}
		if result.AlreadyReported {
			t.Fatal("successful dispatch should not be marked reported")
		}
	})

	t.Run("handler error", func(t *testing.T) {
		var robotHelp = true
		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return errors.New("boom")
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 1 {
			t.Fatalf("error dispatch exit code = %d, want 1", result.ExitCode)
		}
		if result.Err == nil || !strings.Contains(result.Err.Error(), "boom") {
			t.Fatalf("error dispatch returned err = %v, want boom", result.Err)
		}
		if result.AlreadyReported {
			t.Fatal("plain handler errors should not be marked reported")
		}
	})

	t.Run("reported exit", func(t *testing.T) {
		var robotHelp = true
		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return newReportedRobotHandlerExit(2)
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 2 {
			t.Fatalf("reported dispatch exit code = %d, want 2", result.ExitCode)
		}
		if result.Err != nil {
			t.Fatalf("reported exit should not retain wrapped error: %v", result.Err)
		}
		if !result.AlreadyReported {
			t.Fatal("reported exit should preserve AlreadyReported")
		}
	})
}

func TestWriteRobotHelp_ReturnsWriterError(t *testing.T) {
	err := writeRobotHelp(failingWriter{err: errors.New("write failed")})
	if err == nil {
		t.Fatal("expected writer error")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected wrapped writer error, got %v", err)
	}
}

func TestWriteRobotHelp_ReturnsWriterErrorAfterIntro(t *testing.T) {
	writer := &failAfterNWritesWriter{
		failAfter: 1,
		err:       errors.New("write failed after intro"),
	}

	err := writeRobotHelp(writer)
	if err == nil {
		t.Fatal("expected writer error after intro")
	}
	if !strings.Contains(err.Error(), "key bindings") {
		t.Fatalf("expected contextual error for later write, got %v", err)
	}
	if !strings.Contains(err.Error(), "write failed after intro") {
		t.Fatalf("expected underlying writer error, got %v", err)
	}
}

func TestFilterOrphanReportByMinScoreRebuildsDerivedFields(t *testing.T) {
	report := &correlation.OrphanReport{
		Stats: correlation.OrphanReportStats{
			CandidateCount: 2,
			AvgSuspicion:   70,
		},
		Candidates: []correlation.OrphanCandidate{
			{
				ShortSHA:       "aaaaaaa",
				SuspicionScore: 90,
				ProbableBeads:  []correlation.ProbableBead{{BeadID: "bv-keep"}},
			},
			{
				ShortSHA:       "bbbbbbb",
				SuspicionScore: 20,
				ProbableBeads:  []correlation.ProbableBead{{BeadID: "bv-drop"}},
			},
		},
		ByBead: map[string][]string{
			"bv-keep": []string{"aaaaaaa"},
			"bv-drop": []string{"bbbbbbb"},
		},
	}

	filterOrphanReportByMinScore(report, 50)

	if len(report.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(report.Candidates))
	}
	if strings.Compare(report.Candidates[0].ShortSHA, "aaaaaaa") != 0 {
		t.Fatalf("candidate short SHA = %q, want aaaaaaa", report.Candidates[0].ShortSHA)
	}
	if report.Stats.CandidateCount != 1 {
		t.Fatalf("stats candidate count = %d, want 1", report.Stats.CandidateCount)
	}
	if report.Stats.AvgSuspicion != 90 {
		t.Fatalf("avg suspicion = %v, want 90", report.Stats.AvgSuspicion)
	}
	if got := report.ByBead["bv-keep"]; len(got) != 1 || strings.Compare(got[0], "aaaaaaa") != 0 {
		t.Fatalf("kept by_bead entry = %#v, want aaaaaaa", got)
	}
	if dropped := report.ByBead["bv-drop"]; dropped != nil {
		t.Fatalf("dropped candidate still present in by_bead: %#v", dropped)
	}

	filterOrphanReportByMinScore(report, 101)
	if len(report.Candidates) != 0 {
		t.Fatalf("candidate count after filtering all = %d, want 0", len(report.Candidates))
	}
	if report.Stats.CandidateCount != 0 {
		t.Fatalf("stats candidate count after filtering all = %d, want 0", report.Stats.CandidateCount)
	}
	if report.Stats.AvgSuspicion != 0 {
		t.Fatalf("avg suspicion after filtering all = %v, want 0", report.Stats.AvgSuspicion)
	}
	if len(report.ByBead) != 0 {
		t.Fatalf("by_bead after filtering all = %#v, want empty", report.ByBead)
	}
}

func TestParseCorrelationArgTrimsAndRejectsEmptyParts(t *testing.T) {
	commitSHA, beadID, err := parseCorrelationArg("  abc123 : bv-1  ")
	if err != nil {
		t.Fatalf("parseCorrelationArg returned error: %v", err)
	}
	if commitSHA != "abc123" {
		t.Fatalf("commit SHA = %q, want abc123", commitSHA)
	}
	if beadID != "bv-1" {
		t.Fatalf("bead ID = %q, want bv-1", beadID)
	}

	tests := []string{
		"",
		"abc123",
		":bv-1",
		"abc123:",
		"   :   ",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, _, err := parseCorrelationArg(input); err == nil {
				t.Fatalf("parseCorrelationArg(%q) succeeded, want error", input)
			}
		})
	}
}

func TestResolveCorrelatedCommitRejectsAmbiguousPrefix(t *testing.T) {
	commits := []correlation.CorrelatedCommit{
		{SHA: "abc123def456", ShortSHA: "abc123d", Confidence: 0.8},
		{SHA: "abc123fff000", ShortSHA: "abc123f", Confidence: 0.7},
	}

	commit, err := resolveCorrelatedCommit(commits, "abc123d")
	if err != nil {
		t.Fatalf("resolveCorrelatedCommit returned error: %v", err)
	}
	if commit == nil || commit.SHA != "abc123def456" {
		t.Fatalf("resolved commit = %#v, want abc123def456", commit)
	}

	commit, err = resolveCorrelatedCommit(commits, "ABC123F")
	if err != nil {
		t.Fatalf("resolveCorrelatedCommit uppercase short SHA returned error: %v", err)
	}
	if commit == nil || commit.SHA != "abc123fff000" {
		t.Fatalf("uppercase resolved commit = %#v, want abc123fff000", commit)
	}

	commit, err = resolveCorrelatedCommit(commits, "abc123")
	if err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if commit != nil {
		t.Fatalf("commit = %#v, want nil on ambiguity", commit)
	}
	if !strings.Contains(err.Error(), "ambiguous commit SHA prefix") {
		t.Fatalf("error = %q, want ambiguity message", err.Error())
	}
}

func ptrTo[T any](v T) *T {
	return &v
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type failAfterNWritesWriter struct {
	failAfter int
	writes    int
	err       error
}

func (w *failAfterNWritesWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, w.err
	}
	w.writes++
	return len(p), nil
}

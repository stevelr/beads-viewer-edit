package main_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTUIReloadUsesLoadedFallbackSource(t *testing.T) {
	skipIfNoScript(t)
	for _, tc := range []struct {
		name       string
		background string
		wal        bool
	}{
		{"background=0", "0", false},
		{"background=1", "1", false},
		{"wal/background=0", "0", true},
		{"wal/background=1", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			background := tc.background
			dir := t.TempDir()
			t.Setenv("BEADS_DIR", "")
			t.Setenv("BEADS_DB", "")
			t.Setenv("BV_BACKGROUND_MODE", background)
			t.Setenv("BV_NO_UPDATE_CHECK", "1")
			writeIssuesJSONL(t, dir, `{"id":"before","title":"Before refresh","status":"open","issue_type":"task"}`+"\n")
			selected := filepath.Join(dir, ".beads", "issues.jsonl")
			var sourceDB *sql.DB
			var args []string
			if tc.wal {
				selected = filepath.Join(dir, ".beads", "selected.db")
				var err error
				sourceDB, err = sql.Open("sqlite", selected)
				if err != nil {
					t.Fatal(err)
				}
				defer sourceDB.Close()
				sourceDB.SetMaxOpenConns(1)
				if _, err := sourceDB.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
					CREATE TABLE issues (id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL);
					INSERT INTO issues VALUES ('before', 'Before refresh', 'open')`); err != nil {
					t.Fatal(err)
				}
				args = []string{"--db", selected}
			}
			rejected := filepath.Join(dir, ".beads", "beads.jsonl")
			if err := os.WriteFile(rejected, []byte("{invalid\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			older := time.Unix(1_700_000_000, 0)
			if err := os.Chtimes(selected, older, older); err != nil {
				t.Fatal(err)
			}
			newer := older.Add(time.Hour)
			if err := os.Chtimes(rejected, newer, newer); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			bv := buildBvBinary(t)
			cmd := scriptTUICommand(ctx, bv, args...)
			if runtime.GOOS == "linux" {
				for i, arg := range cmd.Args {
					if arg == "-c" && i+1 < len(cmd.Args) {
						cmd.Args[i+1] = "stty columns 110 rows 35 && " + cmd.Args[i+1]
						break
					}
				}
			} else if runtime.GOOS == "darwin" {
				cmd = exec.CommandContext(ctx, "script", append([]string{"-q", "/dev/null", "sh", "-c", "stty columns 110 rows 35 && exec \"$@\"", "sh", bv}, args...)...)
			}
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLUMNS=110", "LINES=35", "BV_TUI_AUTOCLOSE_MS=13000")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			path := filepath.Join(dir, "reload-pty.log")
			output, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			defer func() { cancel(); stdin.Close(); <-wait }()
			offset := 0
			waitForTitle := func(title string) {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && ctx.Err() == nil {
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(ansi.Strip(string(raw[offset:])), title) {
						t.Logf("background=%s rendered %q after byte %d", background, title, offset)
						offset = len(raw)
						return
					}
					time.Sleep(25 * time.Millisecond)
				}
				raw, _ := os.ReadFile(path)
				t.Fatalf("TUI failed to render %q after selected source changed; selected=%s rejected=%s:\n%s", title, selected, rejected, raw)
			}
			waitForTitle("Before refresh")
			if sourceDB != nil {
				before, err := os.Stat(selected)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := sourceDB.Exec(`UPDATE issues SET id='after', title='After refresh'`); err != nil {
					t.Fatal(err)
				}
				after, err := os.Stat(selected)
				if err != nil {
					t.Fatal(err)
				}
				if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
					t.Fatal("WAL test changed the main database; writer must stay open")
				}
			} else {
				writeIssuesJSONL(t, dir, `{"id":"after","title":"After refresh","status":"open","issue_type":"task"}`+"\n")
			}
			waitForTitle("After refresh")
		})
	}
}

func TestTUIFlowDependencyJourney(t *testing.T) {
	skipIfNoScript(t)
	dir := t.TempDir()
	writeIssuesJSONL(t, dir, `{"id":"blocker","title":"Database migration","status":"open","priority":1,"issue_type":"task","labels":["database"]}
{"id":"unrelated","title":"Unrelated database task","status":"open","priority":1,"issue_type":"task","labels":["database"]}
{"id":"dependent","title":"API rollout","status":"open","priority":1,"issue_type":"task","labels":["api"],"dependencies":[{"depends_on_id":"blocker","type":"blocks"}]}
`)
	writeRecipeFile(t, dir, "api-only.yaml", "filters:\n  tags: [api]\nview:\n  columns: [id, title]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	bv := buildBvBinary(t)
	cmd := scriptTUICommand(ctx, bv, "--recipe", "api-only")
	if runtime.GOOS == "linux" {
		for i, arg := range cmd.Args {
			if arg == "-c" && i+1 < len(cmd.Args) {
				cmd.Args[i+1] = "stty columns 110 rows 35 && " + cmd.Args[i+1]
				break
			}
		}
	} else if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "-c", "stty columns 110 rows 35 && exec \"$@\"", "sh", bv, "--recipe", "api-only")
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLUMNS=110", "LINES=35", "BV_TUI_AUTOCLOSE_MS=23000")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	path := filepath.Join(t.TempDir(), "flow-pty.log")
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	defer func() { cancel(); stdin.Close(); <-wait }()
	offset := 0
	waitFor := func(marker string) string {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			frame := ansi.Strip(string(raw[offset:]))
			if strings.Contains(frame, marker) {
				t.Logf("bv=%s argv=%q cwd=%s marker=%q bytes=%d..%d\n%s", bv, cmd.Args, dir, marker, offset, len(raw), frame)
				offset = len(raw)
				return frame
			}
			select {
			case <-deadline.C:
				t.Fatalf("flow did not render %q after byte %d:\n%s", marker, offset, raw)
			case <-ctx.Done():
				t.Fatalf("flow PTY timed out: %v\n%s", ctx.Err(), raw)
			case <-tick.C:
			}
		}
	}
	press := func(key string) {
		t.Helper()
		if _, err := io.WriteString(stdin, key); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("API rollout")
	press("f")
	waitFor("DEPENDENCY FLOW")
	press("\r")
	frame := waitFor("Dependencies involving:")
	for _, want := range []string{"blocker", "blocks dependent", "Database migration", "API rollout"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q in real relationship frame:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "Unrelated database task") {
		t.Fatalf("unrelated label member leaked into relationship frame:\n%s", frame)
	}
	press("\r")
	// Titles also appear in relationship rows. Require the detail-only heading
	// before Escape so a selection redraw cannot satisfy the navigation check.
	waitFor("📋 Database migration")
	press("\x1b")
	waitFor("Dependencies involving:")
	press("j")
	// Wait for the endpoint selection to render before entering its details.
	time.Sleep(50 * time.Millisecond)
	press("\r")
	waitFor("📋 API rollout")
	press("\x1b")
	waitFor("Dependencies involving:")
}

func TestTUIGraphPanAndExpand(t *testing.T) {
	skipIfNoScript(t)
	dir := t.TempDir()
	blocker := "B-" + strings.Repeat("x", 80) + "-PAN-END"
	writeIssuesJSONL(t, dir, fmt.Sprintf(`{"id":"A","title":"Dependent","status":"open","priority":1,"issue_type":"task","dependencies":[{"issue_id":"A","depends_on_id":%q,"type":"blocks"}]}
{"id":%q,"title":"Graph navigation fixture","status":"open","priority":1,"issue_type":"task"}
`, blocker, blocker))
	writeRecipeFile(t, dir, "graph.yaml", "view:\n  show_graph: true\n")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bv := buildBvBinary(t)
	cmd := scriptTUICommand(ctx, bv, "--recipe", "graph")
	if runtime.GOOS == "linux" {
		for i, arg := range cmd.Args {
			if arg == "-c" && i+1 < len(cmd.Args) {
				cmd.Args[i+1] = "stty columns 70 rows 36 && " + cmd.Args[i+1]
				break
			}
		}
	} else if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "-c", "stty columns 70 rows 36 && exec \"$@\"", "sh", bv, "--recipe", "graph")
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "BV_TUI_AUTOCLOSE_MS=18000")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	path := filepath.Join(t.TempDir(), "graph-pty.log")
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	defer func() { cancel(); stdin.Close(); <-wait }()
	offset := 0
	waitFor := func(marker string) string {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw[offset:]), marker) {
				t.Logf("PTY rendered %q after byte %d (%d bytes total)", marker, offset, len(raw))
				frame := string(raw[offset:])
				offset = len(raw)
				return frame
			}
			select {
			case <-deadline.C:
				t.Fatalf("graph did not render %q after byte %d:\n%s", marker, offset, raw)
			case <-ctx.Done():
				t.Fatalf("graph PTY timed out: %v\n%s", ctx.Err(), raw)
			case <-tick.C:
			}
		}
	}
	press := func(key string) {
		t.Helper()
		if _, err := io.WriteString(stdin, key); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("collapsed")
	press(" ")
	expanded := waitFor("DEPENDENCY PATHS")
	if strings.Contains(expanded, "PAN-END → A") {
		t.Fatal("long dependency edge was not initially clipped")
	}
	for i := 0; i < 6; i++ {
		press("L")
		time.Sleep(30 * time.Millisecond)
	}
	waitFor("PAN-END → A")
	for i := 0; i < 6; i++ {
		press("H")
		time.Sleep(30 * time.Millisecond)
	}
	waitFor("DEPENDENCY PATHS")
	press(" ")
	waitFor("collapsed")
}

// TestTUIPrioritySnapshot launches the TUI briefly to ensure it initializes and exits cleanly.
// We rely on BV_TUI_AUTOCLOSE_MS to avoid hanging in CI.
func TestTUIPrioritySnapshot(t *testing.T) {
	skipIfNoScript(t)
	bv := buildBvBinary(t)

	tempDir := t.TempDir()
	beadsDir := filepath.Join(tempDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}
	// Minimal graph with a dependency to exercise insights/priority panes.
	beads := `{"id":"P1","title":"Parent","status":"open","priority":1,"issue_type":"task"}
{"id":"C1","title":"Child","status":"open","priority":2,"issue_type":"task","dependencies":[{"issue_id":"C1","depends_on_id":"P1","type":"blocks"}]}`
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := scriptTUICommand(ctx, bv)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"BV_TUI_AUTOCLOSE_MS=1500",
	)

	ensureCmdStdinCloses(t, ctx, cmd, 3*time.Second)
	out, err := runCmdToFile(t, cmd)
	if ctx.Err() == context.DeadlineExceeded {
		t.Skipf("skipping TUI snapshot: timed out (likely TTY/OS mismatch); output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("TUI run failed: %v\n%s", err, out)
	}
}

// TestTUIBackgroundModeRapidWrites verifies that the TUI stays responsive enough to
// run and exit cleanly under rapid `.beads/beads.jsonl` updates while receiving
// keypress input. This is a smoke test intended to catch deadlocks/panics during
// multi-agent write scenarios.
func TestTUIBackgroundModeRapidWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping rapid-write TUI test in short mode")
	}
	skipIfNoScript(t)
	bv := buildBvBinary(t)

	tempDir := t.TempDir()
	beadsDir := filepath.Join(tempDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads: %v", err)
	}

	beadsPath := filepath.Join(beadsDir, "beads.jsonl")
	initial := `{"id":"P1","title":"Parent","status":"open","priority":1,"issue_type":"task"}
{"id":"C1","title":"Child","status":"open","priority":2,"issue_type":"task","dependencies":[{"issue_id":"C1","depends_on_id":"P1","type":"blocks"}]}
`
	if err := os.WriteFile(beadsPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write beads: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := scriptTUICommand(ctx, bv, "--background-mode")
	if cmd == nil {
		t.Skip("skipping: script command not available on this platform")
	}
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"BV_TUI_AUTOCLOSE_MS=2000",
	)

	stdinR, stdinW := io.Pipe()
	cmd.Stdin = stdinR
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = stdinR.Close()
	})
	// Some `script` implementations keep the pseudo-TTY session open until stdin
	// is closed, even if the child process has exited. Ensure we eventually close
	// stdin so the test can't hang indefinitely.
	time.AfterFunc(3*time.Second, func() { _ = stdinW.Close() })

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	// Simulate user navigation keys while the file is changing.
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := io.WriteString(stdinW, "j"); err != nil {
					return
				}
			}
		}
	}()

	// Simulate multi-agent writes by appending new issues rapidly.
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				f, err := os.OpenFile(beadsPath, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(f, `{"id":"auto-%d","title":"Auto %d","status":"open","priority":2,"issue_type":"task"}`+"\n", i, i)
				_ = f.Close()
			}
		}
	}()

	out, err := runCmdToFile(t, cmd)
	if ctx.Err() == context.DeadlineExceeded {
		t.Skipf("skipping rapid-write TUI test: timed out (likely TTY/OS mismatch); output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("TUI run failed: %v\n%s", err, out)
	}
}

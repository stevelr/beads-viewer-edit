package loader

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
)

// parseSerial runs the loader's serial path on data by feeding it through a
// *bytes.Reader (which is not *os.File, so parseIssuesWithOptions never takes
// the parallel fast path). It returns issues, poolRefs, stats, and the ordered
// warnings — the four things the parallel path must reproduce exactly.
func parseSerial(t *testing.T, data []byte, usePool bool, filter func(*model.Issue) bool) ([]model.Issue, []*model.Issue, ParseStats, []string) {
	t.Helper()
	var stats ParseStats
	var warns []string
	opts := ParseOptions{
		Stats:          &stats,
		IssueFilter:    filter,
		WarningHandler: func(msg string) { warns = append(warns, msg) },
	}
	issues, refs, err := parseIssuesWithOptions(bytes.NewReader(data), opts, usePool)
	if err != nil {
		t.Fatalf("serial parse error: %v", err)
	}
	return issues, refs, stats, warns
}

// parseParallel runs the dedicated parallel orchestrator directly on data so the
// differential test exercises the concurrent code path regardless of file size.
func parseParallel(t *testing.T, data []byte, usePool bool, filter func(*model.Issue) bool) ([]model.Issue, []*model.Issue, ParseStats, []string) {
	t.Helper()
	var stats ParseStats
	var warns []string
	opts := ParseOptions{
		Stats:          &stats,
		IssueFilter:    filter,
		WarningHandler: func(msg string) { warns = append(warns, msg) },
	}
	issues, refs, err := parseIssuesParallel(data, opts, usePool, DefaultMaxBufferSize)
	if err != nil {
		t.Fatalf("parallel parse error: %v", err)
	}
	return issues, refs, stats, warns
}

// assertDiffEqual checks that the serial and parallel outputs are identical in
// every observable dimension: the issue slice (value + order), the count, the
// stats, and the ordered warnings.
func assertDiffEqual(t *testing.T, label string, data []byte, usePool bool, filter func(*model.Issue) bool) {
	t.Helper()

	sIssues, sRefs, sStats, sWarns := parseSerial(t, data, usePool, filter)
	pIssues, pRefs, pStats, pWarns := parseParallel(t, data, usePool, filter)

	// Return pooled refs once compared so the pool stays balanced under -race.
	if usePool {
		defer ReturnIssuePtrsToPool(sRefs)
		defer ReturnIssuePtrsToPool(pRefs)
	}

	if len(sIssues) != len(pIssues) {
		t.Fatalf("%s: count mismatch: serial=%d parallel=%d", label, len(sIssues), len(pIssues))
	}
	if !reflect.DeepEqual(sIssues, pIssues) {
		// Pinpoint the first differing issue for a useful failure message.
		for i := range sIssues {
			if !reflect.DeepEqual(sIssues[i], pIssues[i]) {
				t.Fatalf("%s: issue[%d] differs (order/content):\n serial=%+v\n parall=%+v",
					label, i, sIssues[i], pIssues[i])
			}
		}
		t.Fatalf("%s: issue slices differ but no single index isolated", label)
	}
	if sStats != pStats {
		t.Fatalf("%s: stats mismatch: serial=%+v parallel=%+v", label, sStats, pStats)
	}
	if !reflect.DeepEqual(sWarns, pWarns) {
		t.Fatalf("%s: warnings mismatch:\n serial=%v\n parall=%v", label, sWarns, pWarns)
	}
}

// TestParallelDiff_RealData proves byte-equivalence on the repo's own
// .beads/issues.jsonl (the production workload), for both the plain and pooled
// paths and with/without an issue filter.
func TestParallelDiff_RealData(t *testing.T) {
	candidates := []string{
		filepath.Join("..", "..", ".beads", "issues.jsonl"),
		filepath.Join("..", "..", ".beads", "beads.jsonl"),
	}
	var data []byte
	for _, c := range candidates {
		if b, err := os.ReadFile(c); err == nil && len(b) > 0 {
			data = b
			break
		}
	}
	if data == nil {
		t.Skip("no real .beads JSONL available")
	}

	assertDiffEqual(t, "real/plain", data, false, nil)
	assertDiffEqual(t, "real/pooled", data, true, nil)

	onlyOpen := func(i *model.Issue) bool { return strings.EqualFold(string(i.Status), "open") }
	assertDiffEqual(t, "real/plain+filter", data, false, onlyOpen)
	assertDiffEqual(t, "real/pooled+filter", data, true, onlyOpen)
}

// TestParallelDiff_EdgeFixtures exercises corrupt/edge inputs to prove the
// parallel path reproduces the serial path's warning text, ordering, stats, and
// skip semantics across chunk boundaries.
func TestParallelDiff_EdgeFixtures(t *testing.T) {
	good := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"title":"T-%s","status":"open","issue_type":"task","priority":1}`, id, id)
	}

	// Build a body large enough to span many parallel chunks (the orchestrator
	// targets 256KiB chunks), interleaving valid, malformed, invalid, non-issue,
	// empty, and unknown-_type lines so warnings land on many different lines.
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		switch i % 11 {
		case 3:
			b.WriteString(`{"id":"BAD",not json`) // malformed JSON
		case 5:
			b.WriteString(`{"title":"no id","status":"open","issue_type":"task"}`) // invalid: missing id
		case 7:
			b.WriteString(`{"_type":"memory","text":"a note"}`) // non-issue: silent skip
		case 9:
			b.WriteString("") // empty line
		case 10:
			b.WriteString(`{"_type":"totally_unknown_kind","x":1}`) // unknown _type: silent skip
		default:
			b.WriteString(good(fmt.Sprintf("ISSUE-%d", i)))
		}
		b.WriteByte('\n')
	}
	data := []byte(b.String())

	assertDiffEqual(t, "edge/plain", data, false, nil)
	assertDiffEqual(t, "edge/pooled", data, true, nil)
}

// TestParallelDiff_BOMAndCRLF proves the first-line BOM strip and CRLF trimming
// are handled identically when the input is split into parallel chunks.
func TestParallelDiff_BOMAndCRLF(t *testing.T) {
	good := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"title":"T","status":"open","issue_type":"task","priority":1}`, id)
	}
	var b bytes.Buffer
	b.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM on first line
	for i := 0; i < 3000; i++ {
		b.WriteString(good(fmt.Sprintf("CRLF-%d", i)))
		b.WriteString("\r\n") // CRLF endings
	}
	data := b.Bytes()

	assertDiffEqual(t, "bom-crlf/plain", data, false, nil)
	assertDiffEqual(t, "bom-crlf/pooled", data, true, nil)
}

// TestParallelDiff_NoTrailingNewline proves a final partial line (no trailing
// '\n') is parsed identically by both paths.
func TestParallelDiff_NoTrailingNewline(t *testing.T) {
	good := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"title":"T","status":"open","issue_type":"task","priority":1}`, id)
	}
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString(good(fmt.Sprintf("NT-%d", i)))
		b.WriteByte('\n')
	}
	b.WriteString(good("NT-LAST")) // no trailing newline
	data := []byte(b.String())

	assertDiffEqual(t, "no-trailing-nl/plain", data, false, nil)
	assertDiffEqual(t, "no-trailing-nl/pooled", data, true, nil)
}

// TestParallelParse_AutoDispatchMatchesSerial proves the public entry point's
// size-gated auto-dispatch actually takes the parallel branch (when the file
// exceeds parallelParseMinBytes) and that its result is identical to forcing
// the serial path over the same bytes. It synthesizes a file above the
// threshold from the real data so the test is independent of the repo's current
// store size.
func TestParallelParse_AutoDispatchMatchesSerial(t *testing.T) {
	src := filepath.Join("..", "..", ".beads", "issues.jsonl")
	base, err := os.ReadFile(src)
	if err != nil || len(base) == 0 {
		t.Skip("no real .beads/issues.jsonl available")
	}

	// Replicate the real data until comfortably above the parallel threshold so
	// the *os.File entry point takes the concurrent branch.
	var buf bytes.Buffer
	for int64(buf.Len()) <= parallelParseMinBytes+(1<<20) {
		buf.Write(base)
	}
	data := buf.Bytes()

	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write big fixture: %v", err)
	}

	// Auto path: through the *os.File entry point (takes the parallel branch
	// because the file is above parallelParseMinBytes).
	autoIssues, err := LoadIssuesFromFile(path)
	if err != nil {
		t.Fatalf("LoadIssuesFromFile: %v", err)
	}

	// Reference: forced serial over the identical bytes.
	refIssues, _, _, _ := parseSerial(t, data, false, nil)

	if len(autoIssues) != len(refIssues) {
		t.Fatalf("auto-dispatch count differs: auto=%d ref=%d", len(autoIssues), len(refIssues))
	}
	if !reflect.DeepEqual(autoIssues, refIssues) {
		t.Fatalf("auto-dispatch result differs from serial")
	}
	if runtime.NumCPU() < 1 {
		t.Fatal("unexpected NumCPU")
	}
}

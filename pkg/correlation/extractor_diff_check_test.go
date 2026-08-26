package correlation

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSnapshotMatchesLegacyPatch is a differential test: extractViaSnapshots
// must produce the same BeadEvents as the legacy extractViaGitLogPatch on a real
// repo. Point it at a beads repo via BV_DIFFCHECK_REPO; skipped otherwise.
func TestSnapshotMatchesLegacyPatch(t *testing.T) {
	repo := os.Getenv("BV_DIFFCHECK_REPO")
	if repo == "" {
		t.Skip("set BV_DIFFCHECK_REPO to a beads git repo to run the differential check")
	}
	e := NewExtractor(repo)
	opts := ExtractOptions{Limit: 200}

	legacy, err := e.extractViaGitLogPatch(opts)
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	snap, err := e.extractViaSnapshots(opts)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	key := func(ev BeadEvent) string {
		return fmt.Sprintf("%s|%s|%s|%s", ev.CommitSHA, ev.BeadID, ev.EventType, ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
	}
	ls := make([]string, 0, len(legacy))
	for _, ev := range legacy {
		ls = append(ls, key(ev))
	}
	ss := make([]string, 0, len(snap))
	for _, ev := range snap {
		ss = append(ss, key(ev))
	}
	sort.Strings(ls)
	sort.Strings(ss)

	t.Logf("legacy events=%d snapshot events=%d", len(ls), len(ss))
	if len(ls) != len(ss) {
		t.Fatalf("event count mismatch: legacy=%d snapshot=%d", len(ls), len(ss))
	}
	for i := range ls {
		if ls[i] != ss[i] {
			t.Fatalf("event mismatch at %d:\n legacy=%s\n snap  =%s", i, ls[i], ss[i])
		}
	}
}

func TestBlobReaderReusesOnlyRecycledBuffers(t *testing.T) {
	contentForStatus := func(status string) []byte {
		padding := strings.Repeat("x", 64-len(status))
		return []byte(fmt.Sprintf("{\"id\":\"bv-a\",\"status\":%q,\"title\":%q}\n", status, padding))
	}
	oldContent := contentForStatus("open")
	currentContent := contentForStatus("in_progress")
	nextContent := contentForStatus("closed")

	var protocol bytes.Buffer
	protocol.WriteString("missing-before-first-valid missing\n")
	for i, content := range [][]byte{oldContent, currentContent, nextContent} {
		fmt.Fprintf(&protocol, "oid-%d blob %d\n", i, len(content))
		protocol.Write(content)
		protocol.WriteByte('\n')
	}
	protocol.WriteString("missing-after-recycle missing\n")

	var requests bytes.Buffer
	reader := &blobReader{
		w:           bufio.NewWriter(&requests),
		out:         bufio.NewReaderSize(&protocol, blobReaderBufferSize),
		arenaRunway: blobReaderArenaRunway,
	}

	missing, err := reader.read("missing-before-first-valid")
	if err != nil {
		t.Fatalf("read missing blob before first valid blob: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing blob before first valid blob = %q, want nil", missing)
	}
	if reader.arenaRunway != blobReaderArenaRunway {
		t.Fatalf("missing blob consumed arena runway: got %d, want %d", reader.arenaRunway, blobReaderArenaRunway)
	}

	oldBlob, err := reader.read("oid-0")
	if err != nil {
		t.Fatalf("read old blob: %v", err)
	}
	if got, want := cap(oldBlob), len(oldBlob)+blobReaderArenaRunway; got != want {
		t.Fatalf("first valid blob capacity = %d, want payload %d + runway %d = %d", got, len(oldBlob), blobReaderArenaRunway, want)
	}
	if got, want := reader.out.Size()+cap(oldBlob), gitLogMaxScanTokenSize+len(oldBlob); got != want {
		t.Fatalf("transport + first arena capacity = %d, want prior transport + payload = %d", got, want)
	}
	if reader.arenaRunway != 0 {
		t.Fatalf("first valid blob left arena runway = %d, want 0", reader.arenaRunway)
	}
	oldSet := newRecordLineSet(oldBlob)
	currentBlob, err := reader.read("oid-1")
	if err != nil {
		t.Fatalf("read current blob: %v", err)
	}
	if &oldBlob[0] == &currentBlob[0] {
		t.Fatal("reader reused a buffer whose record-line set was still live")
	}
	currentSet := newRecordLineSet(currentBlob)
	claimed := NewExtractor("/tmp/test").parseDiff(
		synthesizeRecordDiff(oldSet, currentSet), commitInfo{}, "",
	)
	if len(claimed) != 1 || claimed[0].BeadID != "bv-a" || claimed[0].EventType != EventClaimed {
		t.Fatalf("old/current diff changed before recycle: %#v", claimed)
	}

	currentBefore := append([]byte(nil), currentBlob...)
	reader.recycle(oldBlob)
	nextBlob, err := reader.read("oid-2")
	if err != nil {
		t.Fatalf("read next blob: %v", err)
	}
	if &nextBlob[0] != &oldBlob[0] {
		t.Fatal("reader did not reuse the explicitly recycled buffer")
	}
	if cap(nextBlob) != cap(oldBlob) {
		t.Fatalf("reused buffer capacity = %d, want preserved arena capacity %d", cap(nextBlob), cap(oldBlob))
	}
	if !bytes.Equal(currentBlob, currentBefore) {
		t.Fatal("reusing the old buffer overwrote the still-live current blob")
	}
	closed := NewExtractor("/tmp/test").parseDiff(
		synthesizeRecordDiff(currentSet, newRecordLineSet(nextBlob)), commitInfo{}, "",
	)
	if len(closed) != 1 || closed[0].BeadID != "bv-a" || closed[0].EventType != EventClosed {
		t.Fatalf("current/next diff changed after reuse: %#v", closed)
	}

	reader.recycle(nextBlob)
	missing, err = reader.read("missing-after-recycle")
	if err != nil {
		t.Fatalf("read missing blob: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing blob = %q, want nil", missing)
	}
	if cap(reader.spare) == 0 {
		t.Fatal("missing blob consumed the reusable buffer")
	}
}

func TestSnapshotBufferReusePreservesOverlappingHistory(t *testing.T) {
	repo := initTempGitRepo(t)
	t.Setenv("BV_ROBOT", "1")
	t.Setenv("BV_NO_CACHE", "1")
	t.Setenv("BV_CACHE_DIR", t.TempDir())

	beadsDir := filepath.Join(repo, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("create beads dir: %v", err)
	}
	beadsPath := filepath.Join(beadsDir, "issues.jsonl")
	writeState := func(status, message string) {
		t.Helper()
		// Equal-size snapshots force each evicted buffer to be eligible for the
		// next read while the shared boundary blob remains live.
		padding := strings.Repeat("x", 128*1024-len(status))
		content := fmt.Sprintf("{\"id\":\"bv-a\",\"status\":%q,\"title\":%q}\n", status, padding)
		if err := os.WriteFile(beadsPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", status, err)
		}
		runGit(t, repo, "add", ".beads/issues.jsonl")
		runGit(t, repo, "commit", "-m", message)
	}

	writeState("open", "create bead")
	writeState("in_progress", "claim bead")
	writeState("closed", "close bead")
	writeState("open", "reopen bead")

	e := NewExtractor(repo)
	configuredReader, err := e.newBlobReader()
	if err != nil {
		t.Fatalf("create configured blob reader: %v", err)
	}
	if got := configuredReader.out.Size(); got != blobReaderBufferSize {
		t.Errorf("configured blob reader transport capacity = %d, want %d", got, blobReaderBufferSize)
	}
	if got := configuredReader.arenaRunway; got != blobReaderArenaRunway {
		t.Errorf("configured blob reader arena runway = %d, want %d", got, blobReaderArenaRunway)
	}
	if err := configuredReader.Close(); err != nil {
		t.Fatalf("close configured blob reader: %v", err)
	}

	opts := ExtractOptions{Limit: 10}
	legacy, err := e.extractViaGitLogPatch(opts)
	if err != nil {
		t.Fatalf("legacy extraction: %v", err)
	}
	snapshot, err := e.extractViaSnapshots(opts)
	if err != nil {
		t.Fatalf("snapshot extraction: %v", err)
	}
	assertEventsByteIdentical(t, legacy, snapshot, "recycled overlapping window")

	wantTypes := []EventType{EventCreated, EventClaimed, EventClosed, EventReopened}
	if len(snapshot) != len(wantTypes) {
		t.Fatalf("snapshot event count = %d, want %d", len(snapshot), len(wantTypes))
	}
	for i, want := range wantTypes {
		if snapshot[i].BeadID != "bv-a" || snapshot[i].EventType != want {
			t.Fatalf("snapshot event %d = (%q, %q), want (bv-a, %q)", i, snapshot[i].BeadID, snapshot[i].EventType, want)
		}
	}
}

func TestRecordLineSnapshotFrontierMatchesFullBuild(t *testing.T) {
	tests := []struct {
		name           string
		reference      string
		target         string
		wantHashed     int
		wantReused     int
		wantHashBytes  int
		wantReuseBytes int
	}{
		{
			name:       "changed middle",
			reference:  "{\"id\":\"a\"}\n{\"id\":\"middle\",\"v\":1}\n{\"id\":\"z\"}\n",
			target:     "{\"id\":\"a\"}\n{\"id\":\"middle\",\"v\":2}\n{\"id\":\"z\"}\n",
			wantHashed: 1,
			wantReused: 2,
		},
		{
			name:       "equal length unequal records",
			reference:  "{\"id\":\"a\",\"v\":1}\n",
			target:     "{\"id\":\"b\",\"v\":2}\n",
			wantHashed: 1,
			wantReused: 0,
		},
		{
			name:       "single record cannot overlap prefix and suffix",
			reference:  "{\"id\":\"a\"}\n",
			target:     "{\"id\":\"a\"}\n",
			wantHashed: 0,
			wantReused: 1,
		},
		{
			name:       "duplicate boundary records",
			reference:  "{\"id\":\"a\"}\n{\"id\":\"a\"}\n{\"id\":\"z\"}\n",
			target:     "{\"id\":\"a\"}\n{\"id\":\"b\"}\n{\"id\":\"a\"}\n{\"id\":\"z\"}\n",
			wantHashed: 1,
			wantReused: 3,
		},
		{
			name:           "non records CRLF and no final LF",
			reference:      "metadata\r\n{\"id\":\"a\"}\r\nskip\n{\"id\":\"middle\",\"v\":1}\n{\"id\":\"z\"}",
			target:         "different metadata\r\n{\"id\":\"a\"}\r\nskip again\n{\"id\":\"middle\",\"v\":2}\n{\"id\":\"z\"}",
			wantHashed:     1,
			wantReused:     2,
			wantHashBytes:  len("{\"id\":\"middle\",\"v\":2}"),
			wantReuseBytes: len("{\"id\":\"a\"}\r") + len("{\"id\":\"z\"}"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reference, _ := buildRecordLineSnapshot([]byte(tc.reference), nil, hashRecordLine)
			frontier, stats := buildRecordLineSnapshot([]byte(tc.target), &reference, hashRecordLine)
			full, _ := buildRecordLineSnapshot([]byte(tc.target), nil, hashRecordLine)
			assertRecordLineSnapshotsEqual(t, full, frontier)

			if stats.hashedRecords != tc.wantHashed || stats.reusedRecords != tc.wantReused {
				t.Fatalf(
					"frontier work = hashed %d, reused %d; want hashed %d, reused %d",
					stats.hashedRecords, stats.reusedRecords, tc.wantHashed, tc.wantReused,
				)
			}
			if tc.wantHashBytes > 0 && stats.hashedBytes != tc.wantHashBytes {
				t.Fatalf("hashed bytes = %d, want %d", stats.hashedBytes, tc.wantHashBytes)
			}
			if tc.wantReuseBytes > 0 && stats.reusedBytes != tc.wantReuseBytes {
				t.Fatalf("reused bytes = %d, want %d", stats.reusedBytes, tc.wantReuseBytes)
			}
		})
	}
}

func TestRecordLineSnapshotFrontierPreservesForcedCollisionOrder(t *testing.T) {
	constantHash := func([]byte) uint64 { return 7 }
	tests := []struct {
		name      string
		reference string
		target    string
		wantFirst string
	}{
		{
			name:      "reused prefix remains first",
			reference: "{\"id\":\"a\"}\n",
			target:    "{\"id\":\"a\"}\n{\"id\":\"b\"}\n",
			wantFirst: "{\"id\":\"a\"}",
		},
		{
			name:      "hashed prefix precedes reused suffix",
			reference: "{\"id\":\"a\"}\n",
			target:    "{\"id\":\"b\"}\n{\"id\":\"a\"}\n",
			wantFirst: "{\"id\":\"b\"}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reference, _ := buildRecordLineSnapshot([]byte(tc.reference), nil, constantHash)
			frontier, stats := buildRecordLineSnapshot([]byte(tc.target), &reference, constantHash)
			full, _ := buildRecordLineSnapshot([]byte(tc.target), nil, constantHash)
			assertRecordLineSnapshotsEqual(t, full, frontier)

			entry := frontier.lines[constantHash(nil)]
			if entry == nil || entry.count != 2 || string(entry.text) != tc.wantFirst {
				t.Fatalf("colliding entry = %#v, want count 2 first representative %q", entry, tc.wantFirst)
			}
			if stats.hashedRecords != 1 || stats.reusedRecords != 1 {
				t.Fatalf("collision frontier work = %#v, want one hashed and one reused record", stats)
			}
		})
	}
}

func TestRecordLineSnapshotFrontierFallsBackWithoutLiveReference(t *testing.T) {
	target := []byte("{\"id\":\"a\"}\n{\"id\":\"b\"}\n")
	hashCalls := 0
	countingHash := func(line []byte) uint64 {
		hashCalls++
		return uint64(len(line) + hashCalls)
	}

	hole := recordLineSnapshot{blob: append([]byte(nil), target...)}
	_, stats := buildRecordLineSnapshot(target, &hole, countingHash)
	if hashCalls != 2 || stats.hashedRecords != 2 || stats.reusedRecords != 0 {
		t.Fatalf("unindexed reference reused hashes: calls=%d stats=%#v", hashCalls, stats)
	}

	reference, _ := buildRecordLineSnapshot([]byte("{\"id\":\"a\"}\n"), nil, hashRecordLine)
	copy(reference.blob, []byte("{\"id\":\"z\"}\n")) // Model an incorrectly recycled reference arena.
	hashCalls = 0
	_, stats = buildRecordLineSnapshot([]byte("{\"id\":\"a\"}\n"), &reference, countingHash)
	if hashCalls != 1 || stats.hashedRecords != 1 || stats.reusedRecords != 0 {
		t.Fatalf("stale reference digest reused without live-byte equality: calls=%d stats=%#v", hashCalls, stats)
	}
}

func assertRecordLineSnapshotsEqual(t *testing.T, want, got recordLineSnapshot) {
	t.Helper()
	if len(got.lines) != len(want.lines) {
		t.Fatalf("line-set size = %d, want %d", len(got.lines), len(want.lines))
	}
	for hash, wantEntry := range want.lines {
		gotEntry, ok := got.lines[hash]
		if !ok {
			t.Fatalf("line set missing hash %d", hash)
		}
		if gotEntry.count != wantEntry.count || !bytes.Equal(gotEntry.text, wantEntry.text) {
			t.Fatalf(
				"entry %d = count %d text %q, want count %d text %q",
				hash, gotEntry.count, gotEntry.text, wantEntry.count, wantEntry.text,
			)
		}
	}
	if len(got.records) != len(want.records) {
		t.Fatalf("record descriptor count = %d, want %d", len(got.records), len(want.records))
	}
	for i := range want.records {
		if got.records[i] != want.records[i] {
			t.Fatalf("record descriptor %d = %#v, want %#v", i, got.records[i], want.records[i])
		}
	}
}

var benchmarkRecordLineSnapshotSink recordLineSnapshot

func BenchmarkRecordLineSetFrontier(b *testing.B) {
	const (
		recordCount = 8192
		middleStart = recordCount/2 - 8
		middleEnd   = recordCount/2 + 8
	)
	payload := strings.Repeat("record payload ", 16)
	var referenceBlob bytes.Buffer
	var targetBlob bytes.Buffer
	for i := 0; i < recordCount; i++ {
		status := "open"
		targetStatus := status
		if i >= middleStart && i < middleEnd {
			targetStatus = "done"
		}
		fmt.Fprintf(&referenceBlob, "{\"id\":\"bv-%06d\",\"status\":%q,\"title\":%q}\n", i, status, payload)
		fmt.Fprintf(&targetBlob, "{\"id\":\"bv-%06d\",\"status\":%q,\"title\":%q}\n", i, targetStatus, payload)
	}
	referenceBytes := referenceBlob.Bytes()
	targetBytes := targetBlob.Bytes()
	reference, _ := buildRecordLineSnapshot(referenceBytes, nil, hashRecordLine)

	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(targetBytes)))
		var snapshot recordLineSnapshot
		var stats recordLineBuildStats
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snapshot, stats = buildRecordLineSnapshot(targetBytes, nil, hashRecordLine)
		}
		b.StopTimer()
		benchmarkRecordLineSnapshotSink = snapshot
		b.ReportMetric(float64(stats.hashedBytes), "hashed_B/op")
		b.ReportMetric(float64(stats.reusedBytes), "reused_B/op")
	})

	b.Run("frontier", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(targetBytes)))
		var snapshot recordLineSnapshot
		var stats recordLineBuildStats
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snapshot, stats = buildRecordLineSnapshot(targetBytes, &reference, hashRecordLine)
		}
		b.StopTimer()
		benchmarkRecordLineSnapshotSink = snapshot
		b.ReportMetric(float64(stats.hashedBytes), "hashed_B/op")
		b.ReportMetric(float64(stats.reusedBytes), "reused_B/op")
	})
}

package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTutorialProgressPath(t *testing.T) {
	path := TutorialProgressPath()
	if path == "" {
		t.Skip("Could not determine home directory")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("Expected absolute path, got %q", path)
	}
	if filepath.Base(path) != "tutorial-progress.json" {
		t.Errorf("Expected filename tutorial-progress.json, got %q", filepath.Base(path))
	}
}

func TestTutorialProgressManager_Basic(t *testing.T) {
	// Create a fresh manager for testing (bypass singleton)
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	// Test initial state
	if pm.GetViewedCount() != 0 {
		t.Errorf("Expected 0 viewed pages, got %d", pm.GetViewedCount())
	}
	if pm.IsPageViewed("intro") {
		t.Error("Expected intro page to not be viewed initially")
	}
	if pm.HasCompletedOnce() {
		t.Error("Expected not completed initially")
	}

	// Mark page viewed
	pm.MarkPageViewed("intro")
	if !pm.IsPageViewed("intro") {
		t.Error("Expected intro page to be viewed after marking")
	}
	if pm.GetViewedCount() != 1 {
		t.Errorf("Expected 1 viewed page, got %d", pm.GetViewedCount())
	}
	if pm.GetLastPageID() != "intro" {
		t.Errorf("Expected last page to be 'intro', got %q", pm.GetLastPageID())
	}

	// Mark same page again (idempotent)
	pm.MarkPageViewed("intro")
	if pm.GetViewedCount() != 1 {
		t.Errorf("Expected still 1 viewed page, got %d", pm.GetViewedCount())
	}

	// Mark another page
	pm.MarkPageViewed("concepts")
	if pm.GetViewedCount() != 2 {
		t.Errorf("Expected 2 viewed pages, got %d", pm.GetViewedCount())
	}
	if pm.GetLastPageID() != "concepts" {
		t.Errorf("Expected last page to be 'concepts', got %q", pm.GetLastPageID())
	}

	// Test completed flag
	pm.SetCompletedOnce()
	if !pm.HasCompletedOnce() {
		t.Error("Expected completed after setting")
	}
}

func TestTutorialProgressManager_SaveLoad(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Create a manager
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	// Mark some pages viewed
	pm.MarkPageViewed("intro")
	pm.MarkPageViewed("concepts")
	pm.SetCompletedOnce()

	// Save
	if err := pm.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	configPath := filepath.Join(tmpDir, ".config", "bv", "tutorial-progress.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	// Create new manager and load
	pm2 := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}
	if err := pm2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded data
	if !pm2.IsPageViewed("intro") {
		t.Error("Expected intro page to be viewed after load")
	}
	if !pm2.IsPageViewed("concepts") {
		t.Error("Expected concepts page to be viewed after load")
	}
	if !pm2.HasCompletedOnce() {
		t.Error("Expected completed after load")
	}
	if pm2.GetViewedCount() != 2 {
		t.Errorf("Expected 2 viewed pages after load, got %d", pm2.GetViewedCount())
	}
}

func TestTutorialProgressManager_LoadNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	// Load should succeed with empty progress when file doesn't exist
	if err := pm.Load(); err != nil {
		t.Fatalf("Load should succeed for nonexistent file: %v", err)
	}

	if pm.GetViewedCount() != 0 {
		t.Errorf("Expected 0 viewed pages for fresh load, got %d", pm.GetViewedCount())
	}
}

func TestTutorialProgressManager_LoadInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Create invalid JSON file
	configPath := filepath.Join(tmpDir, ".config", "bv", "tutorial-progress.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{invalid"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	// Load should return error but initialize with empty progress
	err := pm.Load()
	if err == nil {
		t.Fatal("Expected error for invalid JSON")
	}

	// Should still be usable with empty progress
	if pm.GetViewedCount() != 0 {
		t.Errorf("Expected 0 viewed pages after invalid JSON load, got %d", pm.GetViewedCount())
	}
}

func TestTutorialProgressManager_Reset(t *testing.T) {
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	pm.MarkPageViewed("intro")
	pm.SetCompletedOnce()

	if pm.GetViewedCount() != 1 {
		t.Errorf("Expected 1 viewed page before reset, got %d", pm.GetViewedCount())
	}

	pm.Reset()

	if pm.GetViewedCount() != 0 {
		t.Errorf("Expected 0 viewed pages after reset, got %d", pm.GetViewedCount())
	}
	if pm.HasCompletedOnce() {
		t.Error("Expected not completed after reset")
	}
	if !pm.IsDirty() {
		t.Error("Expected dirty after reset")
	}
}

func TestTutorialProgressManager_GetProgress(t *testing.T) {
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	pm.MarkPageViewed("intro")
	pm.MarkPageViewed("concepts")

	// Get copy
	progress := pm.GetProgress()

	// Verify copy
	if len(progress.ViewedPages) != 2 {
		t.Errorf("Expected 2 viewed pages in copy, got %d", len(progress.ViewedPages))
	}
	if !progress.ViewedPages["intro"] {
		t.Error("Expected intro in copy")
	}

	// Modify copy should not affect original
	progress.ViewedPages["new"] = true
	if pm.IsPageViewed("new") {
		t.Error("Modifying copy should not affect original")
	}
}

func TestTutorialProgressManager_Concurrent(t *testing.T) {
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	var wg sync.WaitGroup
	pages := []string{"p1", "p2", "p3", "p4", "p5"}

	// Concurrent marks
	for _, p := range pages {
		wg.Add(1)
		go func(pageID string) {
			defer wg.Done()
			pm.MarkPageViewed(pageID)
		}(p)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm.GetViewedCount()
			pm.GetLastPageID()
		}()
	}

	wg.Wait()

	// All pages should be marked
	if pm.GetViewedCount() != len(pages) {
		t.Errorf("Expected %d viewed pages, got %d", len(pages), pm.GetViewedCount())
	}
}

func TestTutorialProgressManager_IsDirty(t *testing.T) {
	pm := &tutorialProgressManager{
		progress: &TutorialProgress{
			ViewedPages: make(map[string]bool),
		},
	}

	if pm.IsDirty() {
		t.Error("Expected not dirty initially")
	}

	pm.MarkPageViewed("intro")
	if !pm.IsDirty() {
		t.Error("Expected dirty after marking page")
	}
}

func TestTutorialProgress_JSONSerialization(t *testing.T) {
	progress := TutorialProgress{
		ViewedPages: map[string]bool{
			"intro":    true,
			"concepts": true,
		},
		LastPageID:    "concepts",
		CompletedOnce: true,
	}

	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var loaded TutorialProgress
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(loaded.ViewedPages) != 2 {
		t.Errorf("Expected 2 viewed pages, got %d", len(loaded.ViewedPages))
	}
	if loaded.LastPageID != "concepts" {
		t.Errorf("Expected last page 'concepts', got %q", loaded.LastPageID)
	}
	if !loaded.CompletedOnce {
		t.Error("Expected completed once")
	}
}

// Tests for TutorialModel integration methods (bv-j4og)

func TestTutorialModel_SaveProgress(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Reset singleton for test isolation
	progressManager = nil
	progressManagerOnce = sync.Once{}

	// Create tutorial model
	theme := Theme{Renderer: lipgloss.DefaultRenderer()}
	m := NewTutorialModel(theme)

	// Navigate to a page (page 1)
	m.NextPage()

	// Save progress
	if err := m.SaveProgress(); err != nil {
		t.Fatalf("SaveProgress failed: %v", err)
	}

	// Verify file was created
	configPath := filepath.Join(tmpDir, ".config", "bv", "tutorial-progress.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Progress file was not created")
	}

	// Verify persisted data
	pm := GetTutorialProgressManager()
	if !pm.IsPageViewed(m.pages[1].ID) {
		t.Error("Expected current page to be marked as viewed")
	}
}

func TestTutorialModel_LoadProgress(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Reset singleton
	progressManager = nil
	progressManagerOnce = sync.Once{}

	// Pre-populate progress file
	pm := GetTutorialProgressManager()
	pm.MarkPageViewed("intro-welcome")
	pm.MarkPageViewed("intro-philosophy")
	if err := pm.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reset singleton to simulate fresh start
	progressManager = nil
	progressManagerOnce = sync.Once{}

	// Create new tutorial model and load progress
	theme := Theme{Renderer: lipgloss.DefaultRenderer()}
	m := NewTutorialModel(theme)
	m.LoadProgress()

	// Verify progress was loaded
	if !m.progress["intro-welcome"] {
		t.Error("Expected intro-welcome to be loaded into model progress")
	}
	if !m.progress["intro-philosophy"] {
		t.Error("Expected intro-philosophy to be loaded into model progress")
	}
}

func TestTutorialModel_HasViewedPage(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Reset singleton
	progressManager = nil
	progressManagerOnce = sync.Once{}

	theme := Theme{Renderer: lipgloss.DefaultRenderer()}
	m := NewTutorialModel(theme)

	// Not viewed initially
	if m.HasViewedPage("intro-welcome") {
		t.Error("Expected intro-welcome to not be viewed initially")
	}

	// Mark as viewed in local progress
	m.progress["intro-welcome"] = true
	if !m.HasViewedPage("intro-welcome") {
		t.Error("Expected intro-welcome to be viewed after local mark")
	}

	// Check persisted state
	pm := GetTutorialProgressManager()
	pm.MarkPageViewed("intro-philosophy")

	if !m.HasViewedPage("intro-philosophy") {
		t.Error("Expected intro-philosophy to be viewed from persisted state")
	}
}

func TestGetTutorialProgressManager_Singleton(t *testing.T) {
	// Reset singleton
	progressManager = nil
	progressManagerOnce = sync.Once{}

	pm1 := GetTutorialProgressManager()
	pm2 := GetTutorialProgressManager()

	if pm1 != pm2 {
		t.Error("GetTutorialProgressManager should return the same instance")
	}
}

func TestTutorialModel_SaveProgress_AllViewed(t *testing.T) {
	// Create temp directory for testing
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")    // exercise the ~/.config fallback path
	t.Setenv("BV_NO_SAVED_CONFIG", "") // TestMain disables persistence; opt back in

	// Reset singleton
	progressManager = nil
	progressManagerOnce = sync.Once{}

	theme := Theme{Renderer: lipgloss.DefaultRenderer()}
	m := NewTutorialModel(theme)

	// Mark all pages as viewed
	pm := GetTutorialProgressManager()
	for _, page := range m.pages {
		pm.MarkPageViewed(page.ID)
	}

	// Save progress - should set completed
	if err := m.SaveProgress(); err != nil {
		t.Fatalf("SaveProgress failed: %v", err)
	}

	if !pm.HasCompletedOnce() {
		t.Error("Expected tutorial to be marked as completed when all pages viewed")
	}
}

// =============================================================================
// Persistence wiring through the TUI model (E3): load on open, mark on page
// change, save on close, resume last page, BV_NO_SAVED_CONFIG disables.
// =============================================================================

// isolatedTutorialProgress points tutorial persistence at a fresh
// XDG_CONFIG_HOME, opts back in to saved config (TestMain disables it), and
// resets the singleton so the test starts from an empty progress file.
// Returns the path the progress file will be written to.
func isolatedTutorialProgress(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("BV_NO_SAVED_CONFIG", "")
	resetTutorialProgressSingleton(t)
	return filepath.Join(xdg, "bv", "tutorial-progress.json")
}

func resetTutorialProgressSingleton(t *testing.T) {
	t.Helper()
	progressManager = nil
	progressManagerOnce = sync.Once{}
	t.Cleanup(func() {
		progressManager = nil
		progressManagerOnce = sync.Once{}
	})
}

func newTutorialTestModel(t *testing.T) *Model {
	t.Helper()
	m := NewModel([]model.Issue{{ID: "tp-1", Title: "Tutorial persistence", Status: model.StatusOpen}}, nil, "")
	t.Cleanup(m.Stop)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(*Model)
}

func pressTutorialKey(t *testing.T, m *Model, key string) *Model {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.Update(msg)
	return updated.(*Model)
}

func TestTutorial_ProgressRoundTrip(t *testing.T) {
	progressPath := isolatedTutorialProgress(t)

	m := newTutorialTestModel(t)
	m = pressTutorialKey(t, m, "`") // open tutorial
	if !m.showTutorial || m.focused != focusTutorial {
		t.Fatalf("expected tutorial open, showTutorial=%v focus=%v", m.showTutorial, m.focused)
	}
	pages := m.tutorialModel.pages
	if len(pages) < 4 {
		t.Fatalf("tutorial fixture too small: %d pages", len(pages))
	}

	m = pressTutorialKey(t, m, "l") // page 2
	m = pressTutorialKey(t, m, "l") // page 3
	if got := m.tutorialModel.currentPage; got != 2 {
		t.Fatalf("after two next-page presses currentPage=%d, want 2", got)
	}
	if _, err := os.Stat(progressPath); !os.IsNotExist(err) {
		t.Fatalf("progress must not be written before the tutorial closes (stat err=%v)", err)
	}

	m = pressTutorialKey(t, m, "q") // close -> SaveProgress
	if m.showTutorial || m.focused != focusList {
		t.Fatalf("expected tutorial closed and list focused, showTutorial=%v focus=%v", m.showTutorial, m.focused)
	}
	if got := m.tutorialModel.currentPage; got != 2 {
		t.Fatalf("closing must keep the tutorial instance (currentPage=%d, want 2)", got)
	}

	data, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("progress file not written on close: %v", err)
	}
	var saved TutorialProgress
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("progress file is not valid JSON: %v\n%s", err, data)
	}
	for _, idx := range []int{0, 1, 2} {
		if !saved.ViewedPages[pages[idx].ID] {
			t.Errorf("page %d (%s) should be persisted as viewed; got %v", idx, pages[idx].ID, saved.ViewedPages)
		}
	}
	if saved.ViewedPages[pages[3].ID] {
		t.Errorf("page 3 (%s) was never shown and must not be persisted as viewed", pages[3].ID)
	}
	if saved.LastPageID != pages[2].ID {
		t.Errorf("last_page_id=%q, want %q", saved.LastPageID, pages[2].ID)
	}

	// Same process, reopening: resumes on the page we left, in place.
	m = pressTutorialKey(t, m, "`")
	if got := m.tutorialModel.currentPage; got != 2 {
		t.Fatalf("reopen in-session: currentPage=%d, want 2", got)
	}
	m = pressTutorialKey(t, m, "q")

	// Fresh process (new singleton, new model): progress and resume point survive.
	resetTutorialProgressSingleton(t)
	m2 := newTutorialTestModel(t)
	if got := m2.tutorialModel.currentPage; got != 2 {
		t.Fatalf("new model should resume at page index 2, got %d", got)
	}
	for _, idx := range []int{0, 1, 2} {
		if !m2.tutorialModel.progress[pages[idx].ID] {
			t.Errorf("new model: page %d should be marked viewed for the TOC", idx)
		}
	}
	m2 = pressTutorialKey(t, m2, "`")
	m2 = pressTutorialKey(t, m2, "t") // show TOC with viewed marks
	view := m2.tutorialModel.View()
	if !strings.Contains(view, "Page 3/") {
		t.Errorf("header should show real page numbers, got:\n%s", view)
	}
	if !strings.Contains(view, "✓") {
		t.Errorf("TOC should mark viewed pages with ✓, got:\n%s", view)
	}
	wantPct := (3 * 100) / len(pages)
	if !strings.Contains(view, fmt.Sprintf("· %d%%", wantPct)) {
		t.Errorf("header should show %d%% viewed, got:\n%s", wantPct, view)
	}
}

func TestTutorial_ResumesLastPage(t *testing.T) {
	isolatedTutorialProgress(t)
	pages := defaultTutorialPages()
	if len(pages) < 7 {
		t.Fatalf("tutorial fixture too small: %d pages", len(pages))
	}

	// Simulate an earlier session that read pages 0..5 and stopped on page 5.
	pm := GetTutorialProgressManager()
	for i := 0; i <= 5; i++ {
		pm.MarkPageViewed(pages[i].ID)
	}
	if err := pm.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	resetTutorialProgressSingleton(t)

	m := newTutorialTestModel(t)
	if got := m.tutorialModel.currentPage; got != 5 {
		t.Fatalf("expected resume at page index 5, got %d", got)
	}

	// Going back to an already-viewed page moves the resume point too.
	m = pressTutorialKey(t, m, "`")
	m = pressTutorialKey(t, m, "h") // page 4
	m = pressTutorialKey(t, m, "esc")
	resetTutorialProgressSingleton(t)
	m2 := newTutorialTestModel(t)
	if got := m2.tutorialModel.currentPage; got != 4 {
		t.Fatalf("resume point should follow backwards navigation: got %d, want 4", got)
	}

	// A stale resume id that no longer matches any page falls back to page 0.
	pm = GetTutorialProgressManager()
	pm.MarkPageViewed("page-that-no-longer-exists")
	if err := pm.Save(); err != nil {
		t.Fatalf("save stale id: %v", err)
	}
	resetTutorialProgressSingleton(t)
	m3 := newTutorialTestModel(t)
	if got := m3.tutorialModel.currentPage; got != 0 {
		t.Fatalf("unknown last page id should fall back to page 0, got %d", got)
	}
}

func TestTutorial_CorruptProgressIgnored(t *testing.T) {
	progressPath := isolatedTutorialProgress(t)
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(progressPath, []byte("{\"viewed_pages\": [garbage"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	m := newTutorialTestModel(t) // must not panic
	if got := m.tutorialModel.currentPage; got != 0 {
		t.Fatalf("corrupt progress should yield defaults, currentPage=%d", got)
	}
	if len(m.tutorialModel.progress) != 0 {
		t.Fatalf("corrupt progress should yield an empty viewed map, got %v", m.tutorialModel.progress)
	}

	// Using the tutorial afterwards replaces the corrupt file with valid JSON.
	m = pressTutorialKey(t, m, "`")
	m = pressTutorialKey(t, m, "l")
	m = pressTutorialKey(t, m, "q")
	data, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read rewritten progress: %v", err)
	}
	var saved TutorialProgress
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("rewritten progress is not valid JSON: %v\n%s", err, data)
	}
	if saved.LastPageID != m.tutorialModel.pages[1].ID {
		t.Fatalf("last_page_id=%q, want %q", saved.LastPageID, m.tutorialModel.pages[1].ID)
	}
}

func TestTutorial_NoSavedConfigWritesNothing(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("BV_NO_SAVED_CONFIG", "1")
	resetTutorialProgressSingleton(t)

	// Seed a real progress file: with persistence disabled it must be ignored.
	seedDir := filepath.Join(xdg, "bv")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pages := defaultTutorialPages()
	seed := TutorialProgress{ViewedPages: map[string]bool{pages[3].ID: true}, LastPageID: pages[3].ID}
	seedBytes, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(seedDir, "tutorial-progress.json"), seedBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := dirListing(t, seedDir)

	m := newTutorialTestModel(t)
	if got := m.tutorialModel.currentPage; got != 0 {
		t.Fatalf("BV_NO_SAVED_CONFIG must not resume from disk, currentPage=%d", got)
	}
	m = pressTutorialKey(t, m, "`")
	m = pressTutorialKey(t, m, "l")
	m = pressTutorialKey(t, m, "l")
	m = pressTutorialKey(t, m, "q")

	after := dirListing(t, seedDir)
	if len(before) != len(after) {
		t.Fatalf("directory listing changed: before=%v after=%v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("directory listing changed: before=%v after=%v", before, after)
		}
	}
	data, err := os.ReadFile(filepath.Join(seedDir, "tutorial-progress.json"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if string(data) != string(seedBytes) {
		t.Fatalf("seed file was rewritten under BV_NO_SAVED_CONFIG:\n%s", data)
	}
}

// dirListing returns "name:size:mtime" entries so any write shows up.
func dirListing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		out = append(out, fmt.Sprintf("%s:%d:%s", e.Name(), info.Size(), info.ModTime().Format(time.RFC3339Nano)))
	}
	return out
}

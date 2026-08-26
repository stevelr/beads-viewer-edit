package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatus_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{"Open", StatusOpen, true},
		{"InProgress", StatusInProgress, true},
		{"Blocked", StatusBlocked, true},
		{"Deferred", StatusDeferred, true},
		{"Pinned", StatusPinned, true},
		{"Hooked", StatusHooked, true},
		{"Review", StatusReview, true},
		{"Closed", StatusClosed, true},
		{"Tombstone", StatusTombstone, true},
		{"Invalid", "unknown", false},
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("Status.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatus_IsClosed(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{"Open", StatusOpen, false},
		{"InProgress", StatusInProgress, false},
		{"Blocked", StatusBlocked, false},
		{"Closed", StatusClosed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsClosed(); got != tt.want {
				t.Errorf("Status.IsClosed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatus_IsOpen(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{"Open", StatusOpen, true},
		{"InProgress", StatusInProgress, true},
		{"Blocked", StatusBlocked, false},
		{"Closed", StatusClosed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsOpen(); got != tt.want {
				t.Errorf("Status.IsOpen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssueType_IsValid(t *testing.T) {
	tests := []struct {
		name      string
		issueType IssueType
		want      bool
	}{
		{"Bug", TypeBug, true},
		{"Feature", TypeFeature, true},
		{"Task", TypeTask, true},
		{"Epic", TypeEpic, true},
		{"Chore", TypeChore, true},
		// Any non-empty type is valid (extensibility for Beads ecosystem)
		{"CustomType", "custom", true},
		// Gastown orchestration types (steveyegge/beads)
		{"GastownRole", "role", true},
		{"GastownAgent", "agent", true},
		{"GastownMolecule", "molecule", true},
		// Only empty is invalid
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.issueType.IsValid(); got != tt.want {
				t.Errorf("IssueType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssueType_IsKnownType(t *testing.T) {
	tests := []struct {
		name      string
		issueType IssueType
		want      bool
	}{
		{"Bug", TypeBug, true},
		{"Feature", TypeFeature, true},
		{"Task", TypeTask, true},
		{"Epic", TypeEpic, true},
		{"Chore", TypeChore, true},
		{"Custom", "custom", false},
		{"GastownRole", "role", false},
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.issueType.IsKnownType(); got != tt.want {
				t.Errorf("IssueType.IsKnownType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDependencyType_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		{"Blocks", DepBlocks, true},
		{"Related", DepRelated, true},
		{"ParentChild", DepParentChild, true},
		{"DiscoveredFrom", DepDiscoveredFrom, true},
		{"Invalid", "causes", false},
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.IsValid(); got != tt.want {
				t.Errorf("DependencyType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDependencyType_IsBlocking(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		{"Blocks", DepBlocks, true},
		{"Related", DepRelated, false},
		{"ParentChild", DepParentChild, false},
		{"Legacy (Empty)", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.IsBlocking(); got != tt.want {
				t.Errorf("DependencyType.IsBlocking() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssue_Struct(t *testing.T) {
	// This test verifies that we can construct an Issue with valid data
	now := time.Now()
	issue := &Issue{
		ID:          "TEST-123",
		Title:       "Test Issue",
		Description: "This is a test issue",
		Status:      StatusOpen,
		Priority:    1, // lower is higher priority
		IssueType:   TypeTask,
		CreatedAt:   now,
		UpdatedAt:   now,
		Labels:      []string{"test", "unit"},
	}

	if issue.ID != "TEST-123" {
		t.Errorf("Issue ID mismatch: got %s, want TEST-123", issue.ID)
	}
	if !issue.Status.IsValid() {
		t.Errorf("Issue Status should be valid")
	}
	if !issue.IssueType.IsValid() {
		t.Errorf("Issue Type should be valid")
	}

	// UpdatedAt should never be before CreatedAt in valid data
	if issue.UpdatedAt.Before(issue.CreatedAt) {
		t.Errorf("UpdatedAt should be >= CreatedAt")
	}
}

func TestDependency_Struct(t *testing.T) {
	now := time.Now()
	dep := &Dependency{
		IssueID:     "A",
		DependsOnID: "B",
		Type:        DepBlocks,
		CreatedAt:   now,
		CreatedBy:   "user",
	}

	if dep.IssueID != "A" {
		t.Errorf("IssueID mismatch")
	}
	if !dep.Type.IsValid() {
		t.Errorf("Dependency type should be valid")
	}
	if !dep.Type.IsBlocking() {
		t.Errorf("DepBlocks should be blocking")
	}
}

func TestDependency_UnmarshalJSON_TargetAliases(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "canonical depends_on_id",
			json: `{"issue_id":"A","depends_on_id":"B","type":"blocks"}`,
			want: "B",
		},
		{
			name: "legacy depends_on",
			json: `{"issue_id":"A","depends_on":"C","type":"blocks"}`,
			want: "C",
		},
		{
			name: "legacy target_id",
			json: `{"issue_id":"A","target_id":"D","type":"blocks"}`,
			want: "D",
		},
		{
			name: "canonical wins over aliases",
			json: `{"issue_id":"A","depends_on_id":"B","depends_on":"C","target_id":"D","type":"blocks"}`,
			want: "B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dep Dependency
			if err := json.Unmarshal([]byte(tt.json), &dep); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if dep.DependsOnID != tt.want {
				t.Fatalf("DependsOnID = %q, want %q", dep.DependsOnID, tt.want)
			}
			if dep.Type != DepBlocks {
				t.Fatalf("Type = %q, want %q", dep.Type, DepBlocks)
			}
		})
	}
}

// unmarshalDependencyStd is the exact pre-optimization implementation. Keep it
// in the test as an executable specification: Dependency.UnmarshalJSON may use
// a faster valid-input decoder, but its values, error behavior, and receiver
// atomicity must remain indistinguishable from encoding/json.
func unmarshalDependencyStd(data []byte, d *Dependency) error {
	type rawDependency struct {
		IssueID     string         `json:"issue_id"`
		DependsOnID string         `json:"depends_on_id"`
		DependsOn   string         `json:"depends_on"`
		TargetID    string         `json:"target_id"`
		Type        DependencyType `json:"type"`
		CreatedAt   time.Time      `json:"created_at"`
		CreatedBy   string         `json:"created_by"`
	}
	var raw rawDependency
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	d.IssueID = raw.IssueID
	d.DependsOnID = raw.DependsOnID
	if d.DependsOnID == "" {
		d.DependsOnID = raw.DependsOn
	}
	if d.DependsOnID == "" {
		d.DependsOnID = raw.TargetID
	}
	d.Type = raw.Type
	d.CreatedAt = raw.CreatedAt
	d.CreatedBy = raw.CreatedBy
	return nil
}

func TestDependency_UnmarshalJSON_MatchesStandardLibrary(t *testing.T) {
	invalidUTF8 := append(
		[]byte(`{"issue_id":"`),
		append([]byte{0xff}, []byte(`","depends_on_id":"B"}`)...)...,
	)
	cases := []struct {
		name string
		data []byte
	}{
		{name: "canonical full", data: []byte(`{"issue_id":"A","depends_on_id":"B","type":"blocks","created_at":"2026-08-24T12:34:56.123456789-04:00","created_by":"agent"}`)},
		{name: "legacy depends_on", data: []byte(`{"issue_id":"A","depends_on":"C","type":"related"}`)},
		{name: "legacy target_id", data: []byte(`{"issue_id":"A","target_id":"D","type":"parent-child"}`)},
		{name: "canonical wins aliases", data: []byte(`{"depends_on_id":"B","depends_on":"C","target_id":"D"}`)},
		{name: "empty canonical falls through", data: []byte(`{"depends_on_id":"","depends_on":"C","target_id":"D"}`)},
		{name: "empty canonical and legacy fall through", data: []byte(`{"depends_on_id":"","depends_on":"","target_id":"D"}`)},
		{name: "duplicate canonical last wins", data: []byte(`{"depends_on_id":"first","depends_on_id":"last"}`)},
		{name: "case folded duplicate last wins", data: []byte(`{"depends_on_id":"exact","DEPENDS_ON_ID":"folded"}`)},
		{name: "case folded then exact last wins", data: []byte(`{"DEPENDS_ON_ID":"folded","depends_on_id":"exact"}`)},
		{name: "escaped key", data: []byte(`{"\u0069ssue_id":"escaped","depends_on_id":"B"}`)},
		{name: "unknown fields", data: []byte(`{"issue_id":"A","depends_on_id":"B","unknown":{"nested":[1,true,null]},"other":"ignored"}`)},
		{name: "missing fields", data: []byte(`{}`)},
		{name: "explicit null fields", data: []byte(`{"issue_id":null,"depends_on_id":null,"type":null,"created_at":null}`)},
		{name: "top level null", data: []byte(`null`)},
		{name: "surrogate replacement", data: []byte(`{"issue_id":"\ud800","depends_on_id":"B"}`)},
		{name: "unicode", data: []byte(`{"issue_id":"café-東京","depends_on_id":"β"}`)},
		{name: "surrounding whitespace", data: []byte(" \n\t{\"issue_id\":\"A\",\"depends_on_id\":\"B\"}\r\n")},
		{name: "wrong issue id type", data: []byte(`{"issue_id":7,"depends_on_id":"B"}`)},
		{name: "wrong dependency type", data: []byte(`{"depends_on_id":{"id":"B"}}`)},
		{name: "wrong alias type", data: []byte(`{"depends_on":false}`)},
		{name: "invalid timestamp", data: []byte(`{"created_at":"not-a-time"}`)},
		{name: "numeric timestamp", data: []byte(`{"created_at":123}`)},
		{name: "duplicate later type error", data: []byte(`{"issue_id":"A","issue_id":9}`)},
		{name: "top level array", data: []byte(`[]`)},
		{name: "top level string", data: []byte(`"dependency"`)},
		{name: "malformed object", data: []byte(`{"issue_id":"A"`)},
		{name: "malformed unknown field", data: []byte(`{"unknown":{"nested":]}`)},
		{name: "trailing junk", data: []byte(`{"issue_id":"A"} trailing`)},
		{name: "invalid utf8", data: invalidUTF8},
	}

	seed := Dependency{
		IssueID:     "seed-issue",
		DependsOnID: "seed-target",
		Type:        DepDiscoveredFrom,
		CreatedAt:   time.Date(2001, time.February, 3, 4, 5, 6, 7, time.UTC),
		CreatedBy:   "seed-author",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := seed
			want := seed
			gotErr := got.UnmarshalJSON(tc.data)
			wantErr := unmarshalDependencyStd(tc.data, &want)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("receiver mismatch:\n candidate=%#v\n stdlib=%#v", got, want)
			}
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("error presence mismatch: candidate=%v stdlib=%v", gotErr, wantErr)
			}
			if gotErr != nil && (reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || gotErr.Error() != wantErr.Error()) {
				t.Fatalf("error mismatch:\n candidate=(%T) %q\n stdlib=(%T) %q", gotErr, gotErr.Error(), wantErr, wantErr.Error())
			}
		})
	}
}

func TestComment_Struct(t *testing.T) {
	now := time.Now()
	comment := &Comment{
		ID:        "1",
		IssueID:   "A",
		Author:    "user",
		Text:      "hello",
		CreatedAt: now,
	}

	if comment.IssueID != "A" {
		t.Errorf("IssueID mismatch")
	}
	if comment.Text != "hello" {
		t.Errorf("Text mismatch")
	}
}

// Regression test for issue #145: beads v1.0+ writes Comment.ID as a
// UUIDv7 string. Earlier versions wrote it as an integer; both shapes
// must round-trip through json.Unmarshal so existing JSONL files keep
// loading after the schema change.
func TestComment_UnmarshalJSON_AcceptsStringAndNumberIDs(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantID string
	}{
		{
			name:   "uuidv7 string id (beads v1.0+)",
			raw:    `{"id":"019d9b8d-e35f-7ce4-9714-d304b1eb90b0","issue_id":"X","author":"a","text":"t","created_at":"2026-04-17T13:07:41Z"}`,
			wantID: "019d9b8d-e35f-7ce4-9714-d304b1eb90b0",
		},
		{
			name:   "integer id (legacy beads)",
			raw:    `{"id":42,"issue_id":"X","author":"a","text":"t","created_at":"2026-04-17T13:07:41Z"}`,
			wantID: "42",
		},
		{
			name:   "missing id",
			raw:    `{"issue_id":"X","author":"a","text":"t","created_at":"2026-04-17T13:07:41Z"}`,
			wantID: "",
		},
		{
			name:   "null id",
			raw:    `{"id":null,"issue_id":"X","author":"a","text":"t","created_at":"2026-04-17T13:07:41Z"}`,
			wantID: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Comment
			if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if c.ID != tc.wantID {
				t.Fatalf("ID: want %q, got %q", tc.wantID, c.ID)
			}
			if c.IssueID != "X" || c.Author != "a" || c.Text != "t" {
				t.Fatalf("non-ID fields not populated: %+v", c)
			}
		})
	}
}

func TestIssue_Validate(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		issue   Issue
		wantErr bool
	}{
		{
			name: "Valid",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "Valid Issue",
				Status:    StatusOpen,
				IssueType: TypeBug,
				CreatedAt: now,
				UpdatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "Empty ID",
			issue: Issue{
				ID:        "",
				Title:     "Valid Issue",
				Status:    StatusOpen,
				IssueType: TypeBug,
			},
			wantErr: true,
		},
		{
			name: "Empty Title",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "",
				Status:    StatusOpen,
				IssueType: TypeBug,
			},
			wantErr: true,
		},
		{
			name: "Invalid Status",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "Valid Issue",
				Status:    "invalid",
				IssueType: TypeBug,
			},
			wantErr: true,
		},
		{
			name: "Empty Type",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "Valid Issue",
				Status:    StatusOpen,
				IssueType: "", // Only empty type is invalid
			},
			wantErr: true,
		},
		{
			name: "Custom Type Allowed",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "Valid Issue",
				Status:    StatusOpen,
				IssueType: "gastown-role", // Non-standard types are now valid
			},
			wantErr: false,
		},
		{
			name: "UpdatedAt Before CreatedAt",
			issue: Issue{
				ID:        "TEST-1",
				Title:     "Valid Issue",
				Status:    StatusOpen,
				IssueType: TypeBug,
				CreatedAt: now,
				UpdatedAt: now.Add(-1 * time.Hour),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.issue.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Issue.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestForecast_Validate(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		forecast Forecast
		wantErr  bool
	}{
		{
			name: "Valid",
			forecast: Forecast{
				BeadID:     "bv-123",
				ETADate:    now.Add(24 * time.Hour),
				Confidence: 0.7,
			},
			wantErr: false,
		},
		{
			name: "Empty BeadID",
			forecast: Forecast{
				BeadID:     "",
				ETADate:    now.Add(24 * time.Hour),
				Confidence: 0.7,
			},
			wantErr: true,
		},
		{
			name: "Zero ETADate",
			forecast: Forecast{
				BeadID:     "bv-123",
				ETADate:    time.Time{},
				Confidence: 0.7,
			},
			wantErr: true,
		},
		{
			name: "Confidence Out Of Range",
			forecast: Forecast{
				BeadID:     "bv-123",
				ETADate:    now.Add(24 * time.Hour),
				Confidence: 1.5,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.forecast.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Forecast.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestForecast_JSON(t *testing.T) {
	eta := time.Date(2025, 2, 1, 12, 0, 0, 0, time.UTC)
	created := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	f := Forecast{
		BeadID:     "bv-123",
		ETADate:    eta,
		Confidence: 0.42,
		Factors:    []string{"label=backend"},
		CreatedAt:  created,
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var roundTrip Forecast
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if roundTrip.BeadID != f.BeadID {
		t.Errorf("BeadID mismatch: got %q, want %q", roundTrip.BeadID, f.BeadID)
	}
	if !roundTrip.ETADate.Equal(f.ETADate) {
		t.Errorf("ETADate mismatch: got %v, want %v", roundTrip.ETADate, f.ETADate)
	}
	if roundTrip.Confidence != f.Confidence {
		t.Errorf("Confidence mismatch: got %v, want %v", roundTrip.Confidence, f.Confidence)
	}
	if len(roundTrip.Factors) != 1 || roundTrip.Factors[0] != "label=backend" {
		t.Errorf("Factors mismatch: got %#v", roundTrip.Factors)
	}
	if !roundTrip.CreatedAt.Equal(f.CreatedAt) {
		t.Errorf("CreatedAt mismatch: got %v, want %v", roundTrip.CreatedAt, f.CreatedAt)
	}

	empty := Forecast{
		BeadID:     "bv-123",
		ETADate:    eta,
		Confidence: 0.42,
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if strings.Contains(string(emptyJSON), "factors") {
		t.Errorf("Expected factors to be omitted when empty: %s", emptyJSON)
	}
	// Note: time.Time with omitempty doesn't omit zero values in Go's JSON encoder.
	// This is a Go limitation - struct types are never considered "empty" for omitempty.
}

func TestBurndownPoint_Validate(t *testing.T) {
	d := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		point   BurndownPoint
		wantErr bool
	}{
		{
			name: "Valid",
			point: BurndownPoint{
				Date:      d,
				Remaining: 10,
				Completed: 5,
			},
			wantErr: false,
		},
		{
			name: "Zero Date",
			point: BurndownPoint{
				Date:      time.Time{},
				Remaining: 10,
				Completed: 5,
			},
			wantErr: true,
		},
		{
			name: "Negative Remaining",
			point: BurndownPoint{
				Date:      d,
				Remaining: -1,
				Completed: 0,
			},
			wantErr: true,
		},
		{
			name: "Negative Completed",
			point: BurndownPoint{
				Date:      d,
				Remaining: 0,
				Completed: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.point.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("BurndownPoint.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBurndownPoint_JSON(t *testing.T) {
	p := BurndownPoint{
		Date:      time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
		Remaining: 10,
		Completed: 5,
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var roundTrip BurndownPoint
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if !roundTrip.Date.Equal(p.Date) || roundTrip.Remaining != p.Remaining || roundTrip.Completed != p.Completed {
		t.Errorf("Round-trip mismatch: got %#v, want %#v", roundTrip, p)
	}
}

// Additional Sprint/Forecast type tests (bv-nnsc)

func TestSprint_Struct(t *testing.T) {
	now := time.Now()
	later := now.AddDate(0, 0, 14)

	sprint := Sprint{
		ID:             "sprint-1",
		Name:           "Test Sprint",
		StartDate:      now,
		EndDate:        later,
		BeadIDs:        []string{"bv-1", "bv-2", "bv-3"},
		VelocityTarget: 25.5,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if sprint.ID != "sprint-1" {
		t.Errorf("Sprint ID mismatch: got %s", sprint.ID)
	}
	if len(sprint.BeadIDs) != 3 {
		t.Errorf("BeadIDs length mismatch: got %d, want 3", len(sprint.BeadIDs))
	}
	if sprint.VelocityTarget != 25.5 {
		t.Errorf("VelocityTarget mismatch: got %f", sprint.VelocityTarget)
	}
}

func TestForecast_Struct(t *testing.T) {
	now := time.Now()
	eta := now.AddDate(0, 0, 5)

	forecast := Forecast{
		BeadID:     "bv-123",
		ETADate:    eta,
		Confidence: 0.75,
		Factors:    []string{"estimate: explicit (120m)", "type: task×1.0"},
		CreatedAt:  now,
	}

	if forecast.BeadID != "bv-123" {
		t.Errorf("BeadID mismatch: got %s", forecast.BeadID)
	}
	if forecast.Confidence != 0.75 {
		t.Errorf("Confidence mismatch: got %f", forecast.Confidence)
	}
	if len(forecast.Factors) != 2 {
		t.Errorf("Factors length mismatch: got %d, want 2", len(forecast.Factors))
	}
	if !forecast.ETADate.Equal(eta) {
		t.Errorf("ETADate mismatch: got %v, want %v", forecast.ETADate, eta)
	}
}

func TestBurndownPoint_Struct(t *testing.T) {
	now := time.Now()

	point := BurndownPoint{
		Date:      now,
		Remaining: 15,
		Completed: 10,
	}

	if point.Remaining != 15 {
		t.Errorf("Remaining mismatch: got %d", point.Remaining)
	}
	if point.Completed != 10 {
		t.Errorf("Completed mismatch: got %d", point.Completed)
	}
	if !point.Date.Equal(now) {
		t.Errorf("Date mismatch")
	}
}

func TestIssue_Clone(t *testing.T) {
	now := time.Now()
	closedAt := now.Add(-1 * time.Hour)
	estimatedMinutes := 60
	externalRef := "JIRA-123"
	compactedAt := now.Add(-2 * time.Hour)
	compactedAtCommit := "abc123"

	original := Issue{
		ID:                "TEST-1",
		Title:             "Test Issue",
		Description:       "Description",
		Status:            StatusOpen,
		Priority:          1,
		IssueType:         TypeBug,
		Assignee:          "user",
		EstimatedMinutes:  &estimatedMinutes,
		CreatedAt:         now,
		UpdatedAt:         now,
		ClosedAt:          &closedAt,
		ExternalRef:       &externalRef,
		CompactedAt:       &compactedAt,
		CompactedAtCommit: &compactedAtCommit,
		Labels:            []string{"bug", "critical"},
		Dependencies: []*Dependency{
			{IssueID: "TEST-1", DependsOnID: "TEST-2", Type: DepBlocks},
		},
		Comments: []*Comment{
			{ID: "1", IssueID: "TEST-1", Author: "user", Text: "comment"},
		},
	}

	clone := original.Clone()

	// Verify basic field equality
	if clone.ID != original.ID {
		t.Errorf("ID mismatch")
	}
	if clone.Title != original.Title {
		t.Errorf("Title mismatch")
	}

	// Verify pointer fields are deep copied
	if clone.EstimatedMinutes == original.EstimatedMinutes {
		t.Errorf("EstimatedMinutes should be a new pointer")
	}
	if *clone.EstimatedMinutes != *original.EstimatedMinutes {
		t.Errorf("EstimatedMinutes value mismatch")
	}

	if clone.ClosedAt == original.ClosedAt {
		t.Errorf("ClosedAt should be a new pointer")
	}
	if !clone.ClosedAt.Equal(*original.ClosedAt) {
		t.Errorf("ClosedAt value mismatch")
	}

	// Verify slice fields are deep copied
	if &clone.Labels == &original.Labels {
		t.Errorf("Labels should be a new slice")
	}
	if len(clone.Labels) != len(original.Labels) {
		t.Errorf("Labels length mismatch")
	}

	// Verify modifying clone doesn't affect original
	*clone.EstimatedMinutes = 120
	if *original.EstimatedMinutes != 60 {
		t.Errorf("Modifying clone affected original EstimatedMinutes")
	}

	clone.Labels[0] = "modified"
	if original.Labels[0] != "bug" {
		t.Errorf("Modifying clone affected original Labels")
	}

	// Verify Dependencies are deep copied
	if len(clone.Dependencies) != 1 {
		t.Errorf("Dependencies length mismatch")
	}
	if clone.Dependencies[0] == original.Dependencies[0] {
		t.Errorf("Dependencies[0] should be a new pointer")
	}

	// Verify Comments are deep copied
	if len(clone.Comments) != 1 {
		t.Errorf("Comments length mismatch")
	}
	if clone.Comments[0] == original.Comments[0] {
		t.Errorf("Comments[0] should be a new pointer")
	}
}

func TestIssue_Clone_NilFields(t *testing.T) {
	original := Issue{
		ID:        "TEST-1",
		Title:     "Test",
		Status:    StatusOpen,
		IssueType: TypeTask,
	}

	clone := original.Clone()

	if clone.EstimatedMinutes != nil {
		t.Errorf("EstimatedMinutes should be nil")
	}
	if clone.ClosedAt != nil {
		t.Errorf("ClosedAt should be nil")
	}
	if clone.Labels != nil {
		t.Errorf("Labels should be nil")
	}
	if clone.Dependencies != nil {
		t.Errorf("Dependencies should be nil")
	}
	if clone.Comments != nil {
		t.Errorf("Comments should be nil")
	}
}

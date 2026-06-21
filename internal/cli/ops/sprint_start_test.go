package ops

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"github.com/robertolupi/botfam/internal/forge"
)

type fakeSprintClient struct {
	issues      map[int]*forge.Issue
	byMilestone map[int64][]*forge.Issue
}

func (f fakeSprintClient) GetIssue(_ context.Context, num int) (*forge.Issue, error) {
	if iss, ok := f.issues[num]; ok {
		return iss, nil
	}
	return nil, fmt.Errorf("issue %d not found", num)
}

func (f fakeSprintClient) ListIssuesByMilestone(_ context.Context, id int64) ([]*forge.Issue, error) {
	return f.byMilestone[id], nil
}

func TestRunSprintStartSeedsIssues(t *testing.T) {
	sessionDir := t.TempDir()
	client := fakeSprintClient{issues: map[int]*forge.Issue{
		12: {Index: 12, Title: "First issue"},
		34: {Index: 34, Title: "Second issue"},
	}}

	genID, members, err := runSprintStart(context.Background(), client, sessionDir, "s1", "botfam/botfam", 0, []int{12, 34})
	if err != nil {
		t.Fatalf("runSprintStart: %v", err)
	}
	if genID != 1 {
		t.Fatalf("first generation id = %d, want 1", genID)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}

	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var gens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scope_generations`).Scan(&gens); err != nil {
		t.Fatal(err)
	}
	if gens != 1 {
		t.Errorf("scope_generations = %d, want 1", gens)
	}

	var members12 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scope_membership WHERE artifact_number = 12 AND disposition = 'in_scope'`).Scan(&members12); err != nil {
		t.Fatal(err)
	}
	if members12 != 1 {
		t.Errorf("scope_membership for #12 = %d, want 1", members12)
	}

	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM work_items WHERE kind = 'resolve_issue' AND state = 'pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 2 {
		t.Errorf("pending work_items = %d, want 2", pending)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM work_items WHERE source_id = '34'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Second issue" {
		t.Errorf("work_item #34 title = %q, want %q", title, "Second issue")
	}

	var transitions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM work_item_state_transitions WHERE to_state = 'pending' AND reason = 'seeded by sprint start'`).Scan(&transitions); err != nil {
		t.Fatal(err)
	}
	if transitions != 2 {
		t.Errorf("seed transitions = %d, want 2", transitions)
	}
}

func TestRunSprintStartAdvancesGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	client := fakeSprintClient{issues: map[int]*forge.Issue{
		7: {Index: 7, Title: "Lucky"},
	}}

	gen1, _, err := runSprintStart(context.Background(), client, sessionDir, "s1", "botfam/botfam", 0, []int{7})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	gen2, _, err := runSprintStart(context.Background(), client, sessionDir, "s1", "botfam/botfam", 0, []int{7})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if gen2 <= gen1 {
		t.Fatalf("second generation %d did not advance past %d", gen2, gen1)
	}

	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var gens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scope_generations`).Scan(&gens); err != nil {
		t.Fatal(err)
	}
	if gens != 2 {
		t.Errorf("scope_generations = %d, want 2", gens)
	}
	// One work item per generation for issue #7 (UNIQUE per generation, so no dup
	// within a generation, but a fresh generation gets a fresh item).
	var items int
	if err := db.QueryRow(`SELECT COUNT(*) FROM work_items WHERE source_id = '7'`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 2 {
		t.Errorf("work_items for #7 = %d, want 2 (one per generation)", items)
	}
}

func TestRunSprintStartMilestone(t *testing.T) {
	sessionDir := t.TempDir()
	client := fakeSprintClient{byMilestone: map[int64][]*forge.Issue{
		42: {
			{Index: 1, Title: "A"},
			{Index: 2, Title: "B"},
			{Index: 3, Title: "C"},
		},
	}}

	_, members, err := runSprintStart(context.Background(), client, sessionDir, "s1", "botfam/botfam", 42, nil)
	if err != nil {
		t.Fatalf("runSprintStart milestone: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want 3", len(members))
	}

	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var srcQuery string
	if err := db.QueryRow(`SELECT source_query FROM scope_generations WHERE id = 1`).Scan(&srcQuery); err != nil {
		t.Fatal(err)
	}
	if srcQuery != "milestone:42" {
		t.Errorf("source_query = %q, want milestone:42", srcQuery)
	}
}

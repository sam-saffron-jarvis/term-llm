package session

import (
	"context"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestBranchParentProjectionsKeepDelegationSeparate(t *testing.T) {
	ctx := context.Background()
	store := newProjectTestStore(t)
	source := &Session{ID: "projection-source", Name: "projection phrase", Provider: "mock", Model: "mock", Mode: ModeChat}
	if err := store.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	anchor := addBranchTestMessage(t, store, source.ID, core.UserText("projection phrase"))
	result, err := store.CreateBranch(ctx, source.ID, CreateBranchOptions{AnchorMessageID: anchor.ID})
	if err != nil {
		t.Fatal(err)
	}
	child := result.Session
	delegated := &Session{ID: "projection-delegated", ParentID: source.ID, Name: "projection phrase", Provider: "mock", Model: "mock", Mode: ModeChat}
	if err := store.Create(ctx, delegated); err != nil {
		t.Fatal(err)
	}
	check := func(projection, id, parent string, number int64) {
		t.Helper()
		wantID, wantNumber := "", int64(0)
		if id == child.ID {
			wantID, wantNumber = source.ID, source.Number
		}
		if parent != wantID || number != wantNumber {
			t.Errorf("%s %s parent = %q #%d, want %q #%d", projection, id, parent, number, wantID, wantNumber)
		}
	}
	for _, getter := range []func() (*Session, error){
		func() (*Session, error) { return store.Get(ctx, child.ID) },
		func() (*Session, error) { return store.GetByNumber(ctx, child.Number) },
		func() (*Session, error) { return store.Get(ctx, delegated.ID) },
	} {
		row, err := getter()
		if err != nil || row == nil {
			t.Fatalf("selected: %v %v", row, err)
		}
		check("selected", row.ID, row.BranchParentSessionID, row.BranchParentNumber)
		if row.ID == delegated.ID && row.ParentID != source.ID {
			t.Fatal("delegation lost")
		}
	}
	rows, err := store.List(ctx, ListOptions{SortByActivity: true})
	if err != nil || len(rows) != 3 {
		t.Fatalf("list: %v %v", rows, err)
	}
	for _, row := range rows {
		check("list", row.ID, row.BranchParentSessionID, row.BranchParentNumber)
	}
	grouped, err := store.Sidebar(ctx, SidebarOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, group := range grouped {
		for _, row := range group.Sessions {
			found = found || row.ID == child.ID
			check("grouped", row.ID, row.BranchParentSessionID, row.BranchParentNumber)
		}
	}
	if !found {
		t.Fatal("grouped sidebar missing child")
	}
	matches, err := store.Search(ctx, SearchOptions{Query: "projection phrase"})
	if err != nil || len(matches) != 3 {
		t.Fatalf("search: %v %v", matches, err)
	}
	for _, row := range matches {
		check("search", row.SessionID, row.BranchParentSessionID, row.BranchParentNumber)
	}
}

func TestBranchParentProjectionWithoutBranchTable(t *testing.T) {
	ctx := context.Background()
	store := newProjectTestStore(t)
	if _, err := store.db.ExecContext(ctx, "DROP TABLE session_branches"); err != nil {
		t.Fatal(err)
	}
	store.hasSessionBranches = false // Same feature detection result as an older read-only store.
	sess := &Session{ID: "old-store", Name: "old projection", Mode: ModeChat}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	row, err := store.Get(ctx, sess.ID)
	if err != nil || row == nil || row.BranchParentSessionID != "" || row.BranchParentNumber != 0 {
		t.Fatalf("get: %+v %v", row, err)
	}
	rows, err := store.List(ctx, ListOptions{})
	if err != nil || len(rows) != 1 || rows[0].BranchParentSessionID != "" || rows[0].BranchParentNumber != 0 {
		t.Fatalf("list: %+v %v", rows, err)
	}
	if _, err := store.Sidebar(ctx, SidebarOptions{}); err != nil {
		t.Fatalf("sidebar: %v", err)
	}
	if matches, err := store.Search(ctx, SearchOptions{Query: "old projection"}); err != nil || len(matches) != 1 {
		t.Fatalf("search: %+v %v", matches, err)
	}
}

package chat

import "testing"

func TestShowSessionListInitializesFilteredAndSelection(t *testing.T) {
	d := NewDialogModel(nil)
	items := []DialogItem{
		{ID: "sess-a", Label: "#1"},
		{ID: "sess-b", Label: "#2"},
	}

	d.ShowSessionList(items, "sess-b")

	if len(d.filtered) != len(items) {
		t.Fatalf("expected filtered list to have %d items, got %d", len(items), len(d.filtered))
	}
	selected := d.Selected()
	if selected == nil {
		t.Fatal("expected selected item to be available")
	}
	if selected.ID != "sess-b" {
		t.Fatalf("expected selected ID %q, got %q", "sess-b", selected.ID)
	}
}

func TestDialogCloseResetsTransientState(t *testing.T) {
	d := NewDialogModel(nil)
	d.ShowSessionList([]DialogItem{{ID: "sess-a", Label: "#1"}}, "")
	d.query = "stale"

	d.Close()

	if d.dialogType != DialogNone {
		t.Fatalf("expected dialog type %v, got %v", DialogNone, d.dialogType)
	}
	if len(d.items) != 0 {
		t.Fatalf("expected items to be cleared, got %d", len(d.items))
	}
	if len(d.filtered) != 0 {
		t.Fatalf("expected filtered items to be cleared, got %d", len(d.filtered))
	}
	if d.query != "" {
		t.Fatalf("expected query to be reset, got %q", d.query)
	}
}

package tools

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testAskQuestion(header string, multiSelect bool) []AskUserQuestion {
	return []AskUserQuestion{
		{
			Header:      header,
			Question:    "Choose an option",
			MultiSelect: multiSelect,
			Options: []AskUserOption{
				{Label: "Option A", Description: "First option"},
				{Label: "Option B", Description: "Second option"},
			},
		},
	}
}

func TestAskUserUI_SingleMultiSelect_EnterSubmitsWhenAnswered_Standalone(t *testing.T) {
	m := newAskModel(testAskQuestion("Q1", true))

	// Select first option.
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(*AskUserModel)

	if len(m.answers[0].selected) != 1 {
		t.Fatalf("expected one selected option after space, got %d", len(m.answers[0].selected))
	}

	// Submit.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*AskUserModel)

	if !m.Done {
		t.Fatal("expected Done=true after pressing enter with a selected option")
	}
	if m.Cancelled {
		t.Fatal("expected Cancelled=false")
	}

	answers := m.Answers()
	if len(answers) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(answers))
	}
	if !answers[0].IsMultiSelect {
		t.Fatal("expected IsMultiSelect=true")
	}
	if len(answers[0].SelectedList) != 1 || answers[0].SelectedList[0] != "Option A" {
		t.Fatalf("expected SelectedList=[Option A], got %#v", answers[0].SelectedList)
	}
}

func TestAskUserUI_SingleMultiSelect_EnterDoesNotSubmitWhenEmpty_Standalone(t *testing.T) {
	m := newAskModel(testAskQuestion("Q1", true))

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*AskUserModel)

	if m.Done {
		t.Fatal("expected Done=false when pressing enter with no selected options")
	}
}

func TestAskUserUI_SingleMultiSelect_SpaceToggles_Standalone(t *testing.T) {
	m := newAskModel(testAskQuestion("Q1", true))

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(*AskUserModel)
	if len(m.answers[0].selected) != 1 || m.answers[0].selected[0] != 0 {
		t.Fatalf("expected selection [0] after first space, got %#v", m.answers[0].selected)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(*AskUserModel)
	if len(m.answers[0].selected) != 0 {
		t.Fatalf("expected empty selection after second space, got %#v", m.answers[0].selected)
	}
}

func TestAskUserUI_SingleMultiSelect_EnterSubmitsWhenAnswered_Embedded(t *testing.T) {
	m := NewEmbeddedAskUserModel(testAskQuestion("Q1", true), 80)

	m.UpdateEmbedded(tea.KeyMsg{Type: tea.KeySpace})
	if len(m.answers[0].selected) != 1 {
		t.Fatalf("expected one selected option after space, got %d", len(m.answers[0].selected))
	}

	m.UpdateEmbedded(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.Done {
		t.Fatal("expected Done=true after pressing enter with a selected option")
	}
}

func TestAskUserUI_MultiQuestionMultiSelect_EnterStillToggles(t *testing.T) {
	questions := []AskUserQuestion{
		{
			Header:      "Q1",
			Question:    "Pick one or more",
			MultiSelect: true,
			Options: []AskUserOption{
				{Label: "Option A", Description: "First option"},
				{Label: "Option B", Description: "Second option"},
			},
		},
		{
			Header:      "Q2",
			Question:    "Pick one",
			MultiSelect: false,
			Options: []AskUserOption{
				{Label: "Choice 1", Description: "First choice"},
				{Label: "Choice 2", Description: "Second choice"},
			},
		},
	}
	m := newAskModel(questions)

	// In multi-question mode, enter should continue toggling selection for multi-select.
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*AskUserModel)

	if m.Done {
		t.Fatal("expected Done=false for multi-question flow")
	}
	if m.currentTab != 0 {
		t.Fatalf("expected to remain on first question tab, got %d", m.currentTab)
	}
	if len(m.answers[0].selected) != 1 || m.answers[0].selected[0] != 0 {
		t.Fatalf("expected selection [0] after enter toggle, got %#v", m.answers[0].selected)
	}
}

func TestAskUserUI_HelpText_SingleMultiSelect_ShowsSubmitHint(t *testing.T) {
	m := newAskModel(testAskQuestion("Q1", true))

	help := m.renderHelp()
	if !strings.Contains(help, "space toggle") {
		t.Fatalf("expected help to contain %q, got %q", "space toggle", help)
	}
	if !strings.Contains(help, "enter submit") {
		t.Fatalf("expected help to contain %q, got %q", "enter submit", help)
	}
}

// ABOUTME: Unit tests for the confirmation TUI in import_keys.go
// ABOUTME: Tests the confirmationTUI.Update state machine with various key inputs and state transitions
package cmd

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConfirmationTUI_Update_EnterWithNoSelected(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press enter while "No" is selected
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	result := model.(confirmationTUI)
	if result.state != cancelling {
		t.Errorf("expected state to be cancelling, got %d", result.state)
	}

	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestConfirmationTUI_Update_YKeyTriggersRestore(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press 'y' key
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	result := model.(confirmationTUI)
	if result.state != confirmed {
		t.Errorf("expected state to be confirmed, got %d", result.state)
	}

	if !result.yes {
		t.Error("expected yes to be true after pressing 'y'")
	}

	if cmd == nil {
		t.Error("expected restoreCmd, got nil")
	}
}

func TestConfirmationTUI_Update_YKeyThenSuccessMsg(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press 'y' key
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	result := model.(confirmationTUI)

	if result.state != confirmed {
		t.Errorf("expected state to be confirmed after 'y', got %d", result.state)
	}

	// Send success message
	model, cmd := result.Update(confirmationSuccessMsg{})
	result = model.(confirmationTUI)

	if result.state != success {
		t.Errorf("expected state to be success after confirmationSuccessMsg, got %d", result.state)
	}

	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestConfirmationTUI_Update_UnrelatedKeyCancels(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press an unrelated key like 'x'
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	result := model.(confirmationTUI)
	if result.state != cancelling {
		t.Errorf("expected state to be cancelling after unrelated key, got %d", result.state)
	}

	if result.yes != false {
		t.Error("expected yes to be false after unrelated key cancels")
	}

	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestConfirmationTUI_Update_LeftToggleSelection(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press left arrow
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	result := model.(confirmationTUI)

	if result.yes != true {
		t.Error("expected yes to toggle to true after left key")
	}

	// Press left arrow again
	model, _ = result.Update(tea.KeyMsg{Type: tea.KeyLeft})
	result = model.(confirmationTUI)

	if result.yes != false {
		t.Error("expected yes to toggle back to false after second left key")
	}
}

func TestConfirmationTUI_Update_RightToggleSelection(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press right arrow
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	result := model.(confirmationTUI)

	if result.yes != false {
		t.Error("expected yes to toggle to false after right key")
	}

	// Press right arrow again
	model, _ = result.Update(tea.KeyMsg{Type: tea.KeyRight})
	result = model.(confirmationTUI)

	if result.yes != true {
		t.Error("expected yes to toggle back to true after second right key")
	}
}

func TestConfirmationTUI_Update_HToggleSelection(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press 'h' key
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	result := model.(confirmationTUI)

	if result.yes != true {
		t.Error("expected yes to toggle to true after 'h' key")
	}

	// Press 'h' key again
	model, _ = result.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	result = model.(confirmationTUI)

	if result.yes != false {
		t.Error("expected yes to toggle back to false after second 'h' key")
	}
}

func TestConfirmationTUI_Update_LToggleSelection(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press 'l' key
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	result := model.(confirmationTUI)

	if result.yes != false {
		t.Error("expected yes to toggle to false after 'l' key")
	}

	// Press 'l' key again
	model, _ = result.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	result = model.(confirmationTUI)

	if result.yes != true {
		t.Error("expected yes to toggle back to true after second 'l' key")
	}
}

func TestConfirmationTUI_Update_EnterWithYesSelected(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press enter while "Yes" is selected
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	result := model.(confirmationTUI)
	if result.state != confirmed {
		t.Errorf("expected state to be confirmed, got %d", result.state)
	}

	if cmd == nil {
		t.Error("expected restoreCmd, got nil")
	}
}

func TestConfirmationTUI_Update_CtrlCCancels(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press ctrl+c
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	result := model.(confirmationTUI)
	if result.state != cancelling {
		t.Errorf("expected state to be cancelling after ctrl+c, got %d", result.state)
	}

	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestConfirmationTUI_Update_ErrorMessage(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    confirmed,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Send error message
	testErr := confirmationErrMsg{error: bytes.ErrTooLarge}
	model, cmd := m.Update(testErr)

	result := model.(confirmationTUI)
	if result.state != fail {
		t.Errorf("expected state to be fail after error message, got %d", result.state)
	}

	if result.err == nil {
		t.Error("expected error to be set")
	}

	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestConfirmationTUI_Update_UnrelatedKeyInNonReadyState(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    confirmed,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	// Press an unrelated key while in confirmed state (not ready)
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	result := model.(confirmationTUI)
	// Should remain in confirmed state (not cancel)
	if result.state != confirmed {
		t.Errorf("expected state to remain confirmed, got %d", result.state)
	}

	// Should still be yes
	if !result.yes {
		t.Error("expected yes to remain true")
	}

	if cmd != nil {
		t.Error("expected no command, got non-nil")
	}
}

func TestConfirmationTUI_Init(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	cmd := m.Init()
	if cmd != nil {
		t.Error("expected Init to return nil, got non-nil")
	}
}

func TestConfirmationTUI_View_ReadyState(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    ready,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view for ready state")
	}
}

func TestConfirmationTUI_View_SuccessState(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    success,
		yes:      true,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view for success state")
	}
}

func TestConfirmationTUI_View_CancellingState(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    cancelling,
		yes:      false,
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view for cancelling state")
	}
}

func TestConfirmationTUI_View_FailState(t *testing.T) {
	m := confirmationTUI{
		reader:   bytes.NewReader(nil),
		state:    fail,
		err:      confirmationErrMsg{error: bytes.ErrTooLarge},
		path:     "/tmp/backup.tar",
		dataPath: "/tmp/data",
	}

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view for fail state")
	}
}

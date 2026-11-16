package proxyconnect

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/fileprivacy"
)

const (
	testGateway    = "https://home-proxy.example.test/claude-code"
	testProxyToken = "tlp_scoped_proxy_token_0123456789abcdef"
	existingClaude = `{
  "permissions": {"allow": ["Bash(ls)"], "deny": []},
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]},
  "enabledPlugins": {"example@market": true},
  "futureUnknownSetting": {"nested": [1, 2.50, "three"]},
  "env": {"EDITOR": "vi", "CLAUDE_CODE_USE_BEDROCK": "0"}
}
`
)

func claudeHome(t *testing.T, settings string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".claude", "settings.json")
	if settings != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, path
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return out
}

func mustApply(t *testing.T, home string) ClaudeSettingsReceipt {
	t.Helper()
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := plan.Apply()
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v (%v), want %v", path, info.Mode().Perm(), err, want)
	}
}

func TestClaudeSettingsMergePreservesEverythingElseAndUndoKeepsLaterEdits(t *testing.T) {
	home, path := claudeHome(t, existingClaude)
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, []string{"PATH=/usr/bin", "CLAUDE_CODE_USE_VERTEX=0"})
	if err != nil {
		t.Fatal(err)
	}
	if untouched, _ := os.ReadFile(path); string(untouched) != existingClaude {
		t.Fatal("planning modified settings")
	}
	if summary := plan.Summary(); strings.Contains(summary, testProxyToken) || !strings.Contains(summary, testGateway) || !strings.Contains(summary, plan.BackupPath) {
		t.Fatalf("summary = %q", summary)
	}
	receipt, err := plan.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.SettingsPath != path || receipt.GatewayURL != testGateway || receipt.CreatedSettings || receipt.BackupPath == "" {
		t.Fatalf("receipt = %+v", receipt)
	}
	written, _ := os.ReadFile(path)
	before, after := map[string]any{}, readJSON(t, path)
	_ = json.Unmarshal([]byte(existingClaude), &before)
	env := after["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != testGateway || env["ANTHROPIC_AUTH_TOKEN"] != testProxyToken || env["EDITOR"] != "vi" || len(env) != 4 {
		t.Fatalf("env = %v", env)
	}
	for _, key := range []string{"permissions", "hooks", "enabledPlugins", "futureUnknownSetting"} {
		want, _ := json.Marshal(before[key])
		got, _ := json.Marshal(after[key])
		if !bytes.Equal(want, got) {
			t.Errorf("%s changed: %s", key, got)
		}
	}
	// Member order and number spelling survive the rewrite.
	if !strings.Contains(string(written), "2.50") || strings.Index(string(written), `"permissions"`) > strings.Index(string(written), `"hooks"`) {
		t.Errorf("unrelated content was reordered or renormalized:\n%s", written)
	}
	assertMode(t, path, 0o600)
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, receipt.BackupPath, 0o600)
	assertMode(t, receipt.ReceiptPath, 0o600)
	if backup, _ := os.ReadFile(receipt.BackupPath); string(backup) != existingClaude {
		t.Error("backup is not the original file")
	}
	if record, _ := os.ReadFile(receipt.ReceiptPath); strings.Contains(string(record), testProxyToken) {
		t.Error("undo record stores the proxy token")
	}

	// Planning again is a no-op; a different token must be undone first.
	if again, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err != nil || !again.AlreadyApplied {
		t.Fatalf("replan: %+v %v", again, err)
	} else if _, err := again.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken+"-rotated", nil); !errors.Is(err, ErrClaudeSettingsConflict) {
		t.Fatalf("second connection: %v", err)
	}

	// A later unrelated edit survives undo; only the gateway keys are removed.
	edited := strings.Replace(string(written), `"EDITOR": "vi"`, `"EDITOR": "hx"`, 1)
	edited = strings.Replace(edited, `"permissions"`, `"model": "opus", "permissions"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	restored := readJSON(t, path)
	env = restored["env"].(map[string]any)
	if restored["model"] != "opus" || env["EDITOR"] != "hx" || len(env) != 2 || restored["hooks"] == nil {
		t.Fatalf("undo lost later edits or kept gateway keys: %v", restored)
	}
	for _, gone := range []string{receipt.BackupPath, receipt.ReceiptPath} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s not removed", gone)
		}
	}
	if err := UndoClaudeSettings(home); !errors.Is(err, ErrClaudeSettingsNotApplied) {
		t.Fatalf("second undo: %v", err)
	}
}

func TestClaudeSettingsCreateAndUndoLeaveNothingBehind(t *testing.T) {
	home, path := claudeHome(t, "")
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil || !plan.CreatesSettings || plan.BackupPath != "" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("planning created ~/.claude")
	}
	receipt, err := plan.Apply()
	if err != nil || !receipt.CreatedSettings {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
	assertMode(t, path, 0o600)
	assertMode(t, filepath.Dir(path), 0o700)
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("created settings file not removed by undo")
	}
}

func TestClaudeSettingsNeverReplacesAnUnrecordedBaseURLOrBearer(t *testing.T) {
	const secret = "previous-upstream-bearer-value-123"
	for name, env := range map[string]string{
		"other bearer":     `"ANTHROPIC_AUTH_TOKEN":"` + secret + `"`,
		"other base url":   `"ANTHROPIC_BASE_URL":"https://` + secret + `.example.test"`,
		"both differ":      `"ANTHROPIC_BASE_URL":"https://older.example.test","ANTHROPIC_AUTH_TOKEN":"` + secret + `"`,
		"url matches only": `"ANTHROPIC_BASE_URL":"` + testGateway + `","ANTHROPIC_AUTH_TOKEN":"` + secret + `"`,
		"non-string":       `"ANTHROPIC_AUTH_TOKEN":12345`,
		"empty bearer":     `"ANTHROPIC_AUTH_TOKEN":""`,
	} {
		content := `{"env":{` + env + `},"model":"sonnet"}`
		home, path := claudeHome(t, content)
		_, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
		if !errors.Is(err, ErrClaudeSettingsConflict) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), testProxyToken) {
			t.Errorf("%s: %v", name, err)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if now, _ := os.ReadFile(path); string(now) != content || len(entries) != 1 {
			t.Errorf("%s: settings were modified or state was written", name)
		}
	}
	// Values that already match are adopted, and undo leaves them as found.
	content := `{"env":{"ANTHROPIC_BASE_URL":"` + testGateway + `","ANTHROPIC_AUTH_TOKEN":"` + testProxyToken + `"},"model":"sonnet"}`
	home, path := claudeHome(t, content)
	mustApply(t, home)
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	restored := readJSON(t, path)
	env := restored["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != testGateway || env["ANTHROPIC_AUTH_TOKEN"] != testProxyToken || restored["model"] != "sonnet" {
		t.Fatalf("pre-existing matching keys not left as found: %v", restored)
	}
}

// A caller that rotated the token in settings by hand, or a damaged record,
// must not be reported as the recorded connection.
func TestClaudeSettingsAlreadyAppliedRequiresAMatchingRecord(t *testing.T) {
	const rotated = testProxyToken + "-rotated"
	home, path := claudeHome(t, existingClaude)
	receipt := mustApply(t, home)
	written, _ := os.ReadFile(path)
	record, _ := os.ReadFile(receipt.ReceiptPath)

	// Settings hold a different token than the record: neither token is accepted.
	if err := os.WriteFile(path, []byte(strings.Replace(string(written), testProxyToken, rotated, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{rotated, testProxyToken} {
		plan, err := PlanClaudeSettings(home, testGateway, token, nil)
		if plan != nil || !errors.Is(err, ErrClaudeSettingsChanged) || strings.Contains(err.Error(), token) {
			t.Fatalf("rotated settings accepted as recorded: %+v %v", plan, err)
		}
	}
	// Record damaged or altered while settings are intact.
	if err := os.WriteFile(path, written, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tampered := range map[string]string{
		"not json":      "{",
		"other version": strings.Replace(string(record), `"version": 1`, `"version": 2`, 1),
		"other digest":  strings.Replace(string(record), tokenDigest(testProxyToken), tokenDigest(rotated), 1),
		"other gateway": strings.Replace(string(record), testGateway, "https://elsewhere.example.test", 1),
	} {
		if err := os.WriteFile(receipt.ReceiptPath, []byte(tampered), 0o600); err != nil {
			t.Fatal(err)
		}
		if plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); plan != nil || !errors.Is(err, ErrClaudeSettingsChanged) {
			t.Errorf("%s: %+v %v", name, plan, err)
		}
	}
	if now, _ := os.ReadFile(path); string(now) != string(written) {
		t.Fatal("settings modified while refusing")
	}
	// The intact record is still recognised.
	if err := os.WriteFile(receipt.ReceiptPath, record, 0o600); err != nil {
		t.Fatal(err)
	}
	if plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err != nil || !plan.AlreadyApplied {
		t.Fatalf("intact record: %+v %v", plan, err)
	}
}

func TestClaudeSettingsUndoRefusesWhenOwnedKeysChanged(t *testing.T) {
	for name, edit := range map[string][2]string{
		"url":   {testGateway, "https://someone-else.example.test"},
		"token": {testProxyToken, "a-different-token-entered-later"},
	} {
		t.Run(name, func(t *testing.T) {
			home, path := claudeHome(t, existingClaude)
			receipt := mustApply(t, home)
			written, _ := os.ReadFile(path)
			changed := strings.Replace(string(written), edit[0], edit[1], 1)
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			err := UndoClaudeSettings(home)
			if !errors.Is(err, ErrClaudeSettingsChanged) || strings.Contains(err.Error(), edit[1]) || strings.Contains(err.Error(), testProxyToken) {
				t.Fatalf("err = %v", err)
			}
			if now, _ := os.ReadFile(path); string(now) != changed {
				t.Fatal("refused undo modified settings")
			}
			if _, err := os.Lstat(receipt.ReceiptPath); err != nil {
				t.Fatal("refused undo discarded its record")
			}
		})
	}
}

func TestClaudeSettingsRejectsCompetingCredentialsWithoutLeakingValues(t *testing.T) {
	const secret = "competing-secret-value-123456"
	processCases := map[string]string{
		"ANTHROPIC_API_KEY": secret, "CLAUDE_CODE_OAUTH_TOKEN": secret, "CLAUDE_CODE_OAUTH_TOKEN_FILE": "/x/" + secret,
		"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "true", "CLAUDE_CODE_USE_FOUNDRY": "1",
		"ANTHROPIC_AUTH_TOKEN": secret, "ANTHROPIC_BASE_URL": "https://" + secret + ".example.test",
		"AWS_BEARER_TOKEN_BEDROCK": secret, "ANTHROPIC_CUSTOM_HEADERS": "x-api-key: " + secret, "CLAUDE_CONFIG_DIR": "/elsewhere/" + secret,
	}
	for key, value := range processCases {
		home, path := claudeHome(t, existingClaude)
		_, err := PlanClaudeSettings(home, testGateway, testProxyToken, []string{"PATH=/usr/bin", key + "=" + value})
		if !errors.Is(err, ErrClaudeSettingsConflict) || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), secret) {
			t.Errorf("process %s: %v", key, err)
		}
		if now, _ := os.ReadFile(path); string(now) != existingClaude {
			t.Errorf("process %s: settings modified", key)
		}
	}
	settingsCases := map[string]string{
		"apiKeyHelper":            `{"apiKeyHelper":"/bin/print-` + secret + `"}`,
		"awsAuthRefresh":          `{"awsAuthRefresh":"aws sso login"}`,
		"ANTHROPIC_API_KEY":       `{"env":{"ANTHROPIC_API_KEY":"` + secret + `"}}`,
		"CLAUDE_CODE_OAUTH_TOKEN": `{"env":{"CLAUDE_CODE_OAUTH_TOKEN":"` + secret + `"}}`,
		"CLAUDE_CODE_USE_BEDROCK": `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`,
		"CLAUDE_CODE_USE_VERTEX":  `{"env":{"CLAUDE_CODE_USE_VERTEX":true}}`,
	}
	for key, settings := range settingsCases {
		home, _ := claudeHome(t, settings)
		_, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
		if !errors.Is(err, ErrClaudeSettingsConflict) || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), secret) {
			t.Errorf("settings %s: %v", key, err)
		}
	}
	// The matching gateway values and the selected config directory are not conflicts.
	home, _ := claudeHome(t, existingClaude)
	same := []string{"ANTHROPIC_BASE_URL=" + testGateway, "ANTHROPIC_AUTH_TOKEN=" + testProxyToken, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, same); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSettingsRejectsUnsafeInputs(t *testing.T) {
	home, _ := claudeHome(t, existingClaude)
	for name, args := range map[string][2]string{
		"upstream credential": {testGateway, "sk-ant-oat01-" + strings.Repeat("a", 40)},
		"short token":         {testGateway, "short"},
		"token whitespace":    {testGateway, "token with spaces 0123456789"},
		"url credentials":     {"https://user:pass@proxy.example.test/claude", testProxyToken},
		"url query":           {"https://proxy.example.test/claude?key=1", testProxyToken},
		"not http":            {"file:///etc/passwd", testProxyToken},
	} {
		if _, err := PlanClaudeSettings(home, args[0], args[1], nil); err == nil || strings.Contains(err.Error(), args[1]) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := PlanClaudeSettings("relative/home", testGateway, testProxyToken, nil); err == nil {
		t.Error("relative home accepted")
	}
}

func TestClaudeSettingsRejectsSymlinksAndSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges")
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(outside, []byte(`{"env":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// settings.json is a symlink.
	home, path := claudeHome(t, existingClaude)
	_ = os.Remove(path)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err == nil {
		t.Error("symlinked settings.json accepted")
	}
	// settings.json is a directory.
	home, path = claudeHome(t, existingClaude)
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err == nil {
		t.Error("directory settings.json accepted")
	}
	// ~/.claude is a symlink.
	home = t.TempDir()
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err == nil {
		t.Error("symlinked ~/.claude accepted")
	}
	// A symlink swapped in after planning is refused at Apply, and so is undo
	// through a symlinked record.
	home, path = claudeHome(t, existingClaude)
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err == nil {
		t.Error("Apply followed a symlink")
	}
	if data, _ := os.ReadFile(outside); string(data) != `{"env":{}}` {
		t.Error("file outside ~/.claude was modified")
	}
	home, _ = claudeHome(t, existingClaude)
	if err := os.Symlink(outside, filepath.Join(home, ".claude", claudeReceiptName)); err != nil {
		t.Fatal(err)
	}
	if err := UndoClaudeSettings(home); err == nil || errors.Is(err, ErrClaudeSettingsNotApplied) {
		t.Errorf("undo through symlinked record: %v", err)
	}
}

func TestClaudeSettingsInvalidOrChangedFilesAreLeftUndamaged(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":     `{"env": {"EDITOR": "vi"`,
		"array":         `[1,2]`,
		"duplicate key": `{"env":{},"env":{"A":"b"}}`,
		"env not map":   `{"env":"ANTHROPIC_BASE_URL=x"}`,
		"trailing":      `{"a":1} {"b":2}`,
	} {
		home, path := claudeHome(t, content)
		if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err == nil {
			t.Errorf("%s: invalid settings accepted", name)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if now, _ := os.ReadFile(path); string(now) != content || len(entries) != 1 {
			t.Errorf("%s: invalid settings were damaged or state was written", name)
		}
	}
	// Edited between plan and approval: nothing is written.
	home, path := claudeHome(t, existingClaude)
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	later := strings.Replace(existingClaude, "vi", "emacs", 1)
	if err := os.WriteFile(path, []byte(later), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); !errors.Is(err, ErrClaudeSettingsChanged) {
		t.Fatalf("stale plan applied: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if now, _ := os.ReadFile(path); string(now) != later || len(entries) != 1 {
		t.Fatal("stale plan wrote state")
	}
	// Empty settings file is an empty object.
	home, path = claudeHome(t, " \n")
	mustApply(t, home)
	if env := readJSON(t, path)["env"].(map[string]any); len(env) != 2 {
		t.Fatalf("env = %v", env)
	}
}

// failClaudeWrites makes writes to paths with the given base name fail. With
// landed set the file is really replaced first, as when a rename succeeds and
// the following directory sync fails.
func failClaudeWrites(t *testing.T, base string, landed bool) {
	t.Helper()
	real := writeClaudeFile
	writeClaudeFile = func(path string, data []byte) error {
		if filepath.Base(path) != base {
			return real(path, data)
		}
		if landed {
			if err := real(path, data); err != nil {
				return err
			}
		}
		return errors.New("injected write failure")
	}
	t.Cleanup(func() { writeClaudeFile = real })
}

func TestClaudeSettingsWriteErrorAfterReplaceKeepsUndoEvidence(t *testing.T) {
	home, path := claudeHome(t, existingClaude)
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	failClaudeWrites(t, "settings.json", true)
	if _, err := plan.Apply(); err == nil || strings.Contains(err.Error(), testProxyToken) {
		t.Fatalf("uncertain write reported as %v", err)
	}
	writeClaudeFile = fileprivacy.WriteFileAtomic
	// The new settings did land, so the backup and record must still exist.
	if backup, _ := os.ReadFile(plan.BackupPath); string(backup) != existingClaude {
		t.Fatal("backup was discarded although settings.json was replaced")
	}
	if _, err := os.Lstat(plan.ReceiptPath); err != nil {
		t.Fatal("undo record was discarded although settings.json was replaced")
	}
	again, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil || !again.AlreadyApplied {
		t.Fatalf("landed change not recognised: %+v %v", again, err)
	}
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	restored := readJSON(t, path)
	if env := restored["env"].(map[string]any); len(env) != 2 || env["EDITOR"] != "vi" || restored["hooks"] == nil {
		t.Fatalf("undo after uncertain write: %v", restored)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("undo left state behind: %d entries", len(entries))
	}
}

func TestClaudeSettingsInterruptedApplyIsResumedOrCleared(t *testing.T) {
	for _, stoppedAt := range []string{claudeBackupName, "settings.json"} {
		t.Run("resume after "+stoppedAt, func(t *testing.T) {
			home, path := claudeHome(t, existingClaude)
			plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
			if err != nil {
				t.Fatal(err)
			}
			failClaudeWrites(t, stoppedAt, false)
			if _, err := plan.Apply(); err == nil {
				t.Fatal("interrupted Apply reported success")
			}
			writeClaudeFile = fileprivacy.WriteFileAtomic
			if now, _ := os.ReadFile(path); string(now) != existingClaude {
				t.Fatal("settings changed although the write failed")
			}
			record, err := os.ReadFile(plan.ReceiptPath)
			if err != nil {
				t.Fatal("intent was not recorded before the failed write")
			}
			// Only the same gateway and token may continue the recorded change.
			if _, err := PlanClaudeSettings(home, testGateway, testProxyToken+"-other", nil); !errors.Is(err, ErrClaudeSettingsConflict) {
				t.Fatalf("another principal resumed the intent: %v", err)
			}
			resumed, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
			if err != nil || resumed.AlreadyApplied {
				t.Fatalf("interrupted intent not resumable: %+v %v", resumed, err)
			}
			receipt, err := resumed.Apply()
			if err != nil {
				t.Fatal(err)
			}
			if now, _ := os.ReadFile(plan.ReceiptPath); string(now) != string(record) {
				t.Error("resume rewrote the recorded intent")
			}
			if backup, _ := os.ReadFile(receipt.BackupPath); string(backup) != existingClaude {
				t.Error("resume lost the original backup")
			}
			if env := readJSON(t, path)["env"].(map[string]any); env["ANTHROPIC_AUTH_TOKEN"] != testProxyToken {
				t.Fatalf("resume did not apply: %v", env)
			}
			if err := UndoClaudeSettings(home); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("undo clears a prepared intent", func(t *testing.T) {
		home, path := claudeHome(t, existingClaude)
		plan, _ := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
		failClaudeWrites(t, "settings.json", false)
		if _, err := plan.Apply(); err == nil {
			t.Fatal("interrupted Apply reported success")
		}
		writeClaudeFile = fileprivacy.WriteFileAtomic
		unrelated := filepath.Join(filepath.Dir(path), "keep-me.json")
		if err := os.WriteFile(unrelated, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := UndoClaudeSettings(home); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if now, _ := os.ReadFile(path); string(now) != existingClaude || len(entries) != 2 {
			t.Fatalf("prepared intent not cleared cleanly: %d entries", len(entries))
		}
	})

	t.Run("settings edited after a prepared intent", func(t *testing.T) {
		home, path := claudeHome(t, existingClaude)
		plan, _ := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
		failClaudeWrites(t, "settings.json", false)
		_, _ = plan.Apply()
		writeClaudeFile = fileprivacy.WriteFileAtomic
		edited := strings.Replace(existingClaude, "vi", "hx", 1)
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); !errors.Is(err, ErrClaudeSettingsChanged) {
			t.Fatalf("plan: %v", err)
		}
		if err := UndoClaudeSettings(home); !errors.Is(err, ErrClaudeSettingsChanged) {
			t.Fatalf("undo: %v", err)
		}
		if now, _ := os.ReadFile(path); string(now) != edited {
			t.Fatal("settings modified while refusing")
		}
	})
}

func TestClaudeSettingsNeverOverwritesOrRemovesAForeignBackup(t *testing.T) {
	const foreign = `{"someone":"else's backup"}`
	// No record of ours: the fixed backup name is taken, so nothing is planned.
	home, path := claudeHome(t, existingClaude)
	backupPath := filepath.Join(filepath.Dir(path), claudeBackupName)
	if err := os.WriteFile(backupPath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); !errors.Is(err, ErrClaudeSettingsConflict) {
		t.Fatalf("backup collision: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if kept, _ := os.ReadFile(backupPath); string(kept) != foreign || len(entries) != 2 {
		t.Fatal("foreign backup touched or state written")
	}
	if now, _ := os.ReadFile(path); string(now) != existingClaude {
		t.Fatal("settings modified")
	}
	// It appears between plan and Apply.
	_ = os.Remove(backupPath)
	plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); !errors.Is(err, ErrClaudeSettingsChanged) {
		t.Fatalf("Apply over a foreign backup: %v", err)
	}
	if _, err := os.Lstat(plan.ReceiptPath); !os.IsNotExist(err) {
		t.Fatal("intent recorded although Apply refused")
	}
	// A prepared intent whose backup was replaced: resume refuses, and undo
	// clears only the record.
	_ = os.Remove(backupPath)
	plan, _ = PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	failClaudeWrites(t, "settings.json", false)
	_, _ = plan.Apply()
	writeClaudeFile = fileprivacy.WriteFileAtomic
	if err := os.WriteFile(backupPath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Apply(); !errors.Is(err, ErrClaudeSettingsChanged) {
		t.Fatalf("resume over a foreign backup: %v", err)
	}
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	if kept, _ := os.ReadFile(backupPath); string(kept) != foreign {
		t.Fatal("undo removed or changed a backup it could not verify")
	}
	if _, err := os.Lstat(plan.ReceiptPath); !os.IsNotExist(err) {
		t.Fatal("undo left its record")
	}
	if now, _ := os.ReadFile(path); string(now) != existingClaude {
		t.Fatal("settings modified")
	}
}

// Records written before hashes existed still undo an applied connection, but
// an unapplied one cannot be told apart from an edited file and is not resumed.
func TestClaudeSettingsRecordsWithoutHashesStillUndoButDoNotResume(t *testing.T) {
	stripHashes := func(t *testing.T, receiptPath string) {
		t.Helper()
		var record map[string]any
		data, _ := os.ReadFile(receiptPath)
		if err := json.Unmarshal(data, &record); err != nil || record["original_sha256"] == nil || record["updated_sha256"] == nil {
			t.Fatalf("record lacks hashes: %s", data)
		}
		delete(record, "original_sha256")
		delete(record, "updated_sha256")
		data, _ = json.Marshal(record)
		if err := os.WriteFile(receiptPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, path := claudeHome(t, existingClaude)
	receipt := mustApply(t, home)
	stripHashes(t, receipt.ReceiptPath)
	if plan, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); err != nil || !plan.AlreadyApplied {
		t.Fatalf("old applied record: %+v %v", plan, err)
	}
	if err := UndoClaudeSettings(home); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatal("old record undo left state behind")
	}

	home, _ = claudeHome(t, existingClaude)
	plan, _ := PlanClaudeSettings(home, testGateway, testProxyToken, nil)
	failClaudeWrites(t, "settings.json", false)
	_, _ = plan.Apply()
	writeClaudeFile = fileprivacy.WriteFileAtomic
	stripHashes(t, plan.ReceiptPath)
	if _, err := PlanClaudeSettings(home, testGateway, testProxyToken, nil); !errors.Is(err, ErrClaudeSettingsChanged) {
		t.Fatalf("ambiguous old intent resumed: %v", err)
	}
	if err := UndoClaudeSettings(home); !errors.Is(err, ErrClaudeSettingsChanged) {
		t.Fatalf("ambiguous old intent undone: %v", err)
	}
}

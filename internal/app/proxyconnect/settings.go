package proxyconnect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/term-llm/term-llm/internal/fileprivacy"
)

const (
	claudeSettingsMaxBytes = 1 << 20
	claudeBaseURLKey       = "ANTHROPIC_BASE_URL"
	claudeAuthTokenKey     = "ANTHROPIC_AUTH_TOKEN"
	claudeBackupName       = "settings.json.term-llm-proxy-backup"
	claudeReceiptName      = "term-llm-proxy-connect.json"
)

var (
	// ErrClaudeSettingsConflict reports another credential or routing choice
	// that would compete with the gateway. The message names the setting only.
	ErrClaudeSettingsConflict = errors.New("conflicting Claude credential or routing setting")
	// ErrClaudeSettingsChanged reports that settings no longer hold what was
	// planned or applied; nothing was modified.
	ErrClaudeSettingsChanged = errors.New("Claude settings changed; nothing was modified")
	// ErrClaudeSettingsNotApplied reports that there is nothing to undo.
	ErrClaudeSettingsNotApplied = errors.New("no term-llm gateway connection is recorded for Claude")
)

// Credential and routing choices that compete with a gateway bearer. A cloud
// switch conflicts only when enabled; every other entry conflicts when set.
var (
	claudeConflictingEnv = []string{
		"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE", "CLAUDE_CODE_API_KEY_HELPER_FD",
		"ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_FOUNDRY_BASE_URL",
		"ANTHROPIC_FOUNDRY_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "ANTHROPIC_CUSTOM_HEADERS",
	}
	claudeCloudSwitches       = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}
	claudeConflictingSettings = []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "forceLoginMethod"}
)

// ClaudeSettingsPlan is a validated, not yet applied change to the user's
// ~/.claude/settings.json. Its exported fields contain no secret.
type ClaudeSettingsPlan struct {
	SettingsPath, BackupPath, ReceiptPath, GatewayURL string
	CreatesSettings                                   bool // No settings.json exists yet.
	AlreadyApplied                                    bool // Settings already hold this connection; Apply changes nothing.

	original, updated, receipt []byte
	resumed                    bool // The undo record already exists from an interrupted Apply.
}

// ClaudeSettingsReceipt describes an applied connection. BackupPath is empty
// when no settings file existed before.
type ClaudeSettingsReceipt struct {
	SettingsPath, BackupPath, ReceiptPath, GatewayURL string
	CreatedSettings                                   bool
}

// claudeReceiptFile is the private undo record. Previous values are the raw
// JSON of the two owned keys before the change; null means the key was absent.
type claudeReceiptFile struct {
	Version         int             `json:"version"`
	GatewayURL      string          `json:"gateway_url"`
	TokenSHA256     string          `json:"token_sha256"`
	CreatedSettings bool            `json:"created_settings"`
	CreatedEnv      bool            `json:"created_env"`
	PreviousBaseURL json.RawMessage `json:"previous_base_url"`
	PreviousToken   json.RawMessage `json:"previous_auth_token"`
	// Hashes of settings.json before and after the change. They let an
	// interrupted Apply be recognised and resumed or cleared. Records written
	// before these existed leave them empty and cannot be resumed.
	OriginalSHA256 string `json:"original_sha256,omitempty"`
	UpdatedSHA256  string `json:"updated_sha256,omitempty"`
}

// matchesOriginal reports whether settings.json is still exactly what it was
// before the recorded change, including not existing at all.
func (r claudeReceiptFile) matchesOriginal(current []byte, exists bool) bool {
	return r.OriginalSHA256 != "" && exists != r.CreatedSettings && tokenDigest(string(current)) == r.OriginalSHA256
}

// writeClaudeFile replaces a private file atomically. An error does not prove
// the file is unchanged: the rename may have succeeded before a later sync failed.
var writeClaudeFile = fileprivacy.WriteFileAtomic

// orderedObject is a JSON object that keeps member order and raw values, so
// unrelated settings survive a rewrite untouched apart from whitespace.
type orderedObject struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseOrderedObject(data []byte) (*orderedObject, error) {
	object := &orderedObject{values: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return object, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, errors.New("invalid JSON")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid JSON")
		}
		if _, duplicate := object.values[key]; duplicate {
			return nil, errors.New("duplicate JSON member")
		}
		object.keys = append(object.keys, key)
		object.values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("invalid JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON content")
	}
	return object, nil
}

func (o *orderedObject) set(key string, value json.RawMessage) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *orderedObject) remove(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, existing := range o.keys {
		if existing == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

func (o *orderedObject) compact() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		b.Write(name)
		b.WriteByte(':')
		b.Write(o.values[key])
	}
	b.WriteByte('}')
	return b.Bytes()
}

func (o *orderedObject) document() []byte {
	var b bytes.Buffer
	_ = json.Indent(&b, o.compact(), "", "  ")
	b.WriteByte('\n')
	return b.Bytes()
}

// jsonString returns the string held by a raw JSON value, if it is one.
func jsonString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// claudeSettingsPaths validates home and returns the selected user's Claude
// directory. Neither home nor the directory may be a symlink.
func claudeSettingsDir(home string) (string, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", errors.New("home must be a clean absolute path")
	}
	if info, err := os.Lstat(home); err != nil || !info.IsDir() {
		return "", errors.New("home is not a directory")
	}
	dir := filepath.Join(home, ".claude")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return dir, nil
	}
	if err != nil || !info.IsDir() {
		return "", errors.New("~/.claude must be a real directory, not a symlink or file")
	}
	if !ownedByCurrentUser(info) {
		return "", errors.New("~/.claude is owned by another user")
	}
	return dir, nil
}

// readPrivateRegular reads a bounded regular file. A missing file is (nil,
// false, nil); a symlink or any other non-regular file is refused.
func readPrivateRegular(path, label string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s must be a regular file, not a symlink or special file", label)
	}
	if !ownedByCurrentUser(info) {
		return nil, false, fmt.Errorf("%s is owned by another user", label)
	}
	if info.Size() > claudeSettingsMaxBytes {
		return nil, false, fmt.Errorf("%s is too large", label)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", label, err)
	}
	return data, true, nil
}

// envOf returns the settings env object and whether the member exists.
func envOf(settings *orderedObject) (*orderedObject, bool, error) {
	raw, ok := settings.values["env"]
	if !ok {
		return &orderedObject{values: map[string]json.RawMessage{}}, false, nil
	}
	env, err := parseOrderedObject(raw)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, true, errors.New("settings.json env is not a JSON object")
	}
	return env, true, nil
}

func claudeSwitchEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func conflict(where, name string) error {
	return fmt.Errorf("%w: %s %s", ErrClaudeSettingsConflict, where, name)
}

func checkClaudeProcessEnv(env []string, dir, gatewayURL, proxyToken string) error {
	values := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	for _, key := range claudeConflictingEnv {
		if values[key] != "" {
			return conflict("environment variable", key)
		}
	}
	for _, key := range claudeCloudSwitches {
		if claudeSwitchEnabled(values[key]) {
			return conflict("environment variable", key)
		}
	}
	if v := values[claudeBaseURLKey]; v != "" && v != gatewayURL {
		return conflict("environment variable", claudeBaseURLKey)
	}
	if v := values[claudeAuthTokenKey]; v != "" && v != proxyToken {
		return conflict("environment variable", claudeAuthTokenKey)
	}
	if v := values["CLAUDE_CONFIG_DIR"]; v != "" && filepath.Clean(v) != dir {
		return conflict("environment variable", "CLAUDE_CONFIG_DIR")
	}
	return nil
}

func checkClaudeSettingsConflicts(settings, env *orderedObject) error {
	for _, key := range claudeConflictingSettings {
		if raw, ok := settings.values[key]; ok && string(bytes.TrimSpace(raw)) != "null" {
			return conflict("settings.json", key)
		}
	}
	for _, key := range claudeConflictingEnv {
		if raw, ok := env.values[key]; ok {
			if value, isString := jsonString(raw); !isString || value != "" {
				return conflict("settings.json env", key)
			}
		}
	}
	for _, key := range claudeCloudSwitches {
		if raw, ok := env.values[key]; ok {
			if value, isString := jsonString(raw); !isString || claudeSwitchEnabled(value) {
				return conflict("settings.json env", key)
			}
		}
	}
	return nil
}

func validateClaudeGateway(gatewayURL, proxyToken string) error {
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("gateway URL must be an http(s) URL without credentials, query or fragment")
	}
	if len(proxyToken) < 16 || strings.ContainsFunc(proxyToken, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return errors.New("proxy token must be at least 16 characters without whitespace")
	}
	if strings.HasPrefix(proxyToken, "sk-ant-") {
		return errors.New("an Anthropic credential was supplied where the scoped proxy token belongs")
	}
	return nil
}

// PlanClaudeSettings validates and prepares, without writing anything, the
// minimal change that points the selected user's Claude Code at the gateway:
// env.ANTHROPIC_BASE_URL and env.ANTHROPIC_AUTH_TOKEN (the scoped proxy token,
// never an upstream credential) in home/.claude/settings.json. Every other
// setting is preserved, and an existing different value of either key is a
// conflict rather than something to overwrite. env is the process environment
// to check for competing credentials. Errors never contain a token or a
// setting's value. If an earlier Apply for this same gateway and token was
// interrupted before settings.json changed, the returned plan resumes it.
func PlanClaudeSettings(home, gatewayURL, proxyToken string, env []string) (*ClaudeSettingsPlan, error) {
	if err := validateClaudeGateway(gatewayURL, proxyToken); err != nil {
		return nil, err
	}
	dir, err := claudeSettingsDir(home)
	if err != nil {
		return nil, err
	}
	if err := checkClaudeProcessEnv(env, dir, gatewayURL, proxyToken); err != nil {
		return nil, err
	}
	plan := &ClaudeSettingsPlan{
		SettingsPath: filepath.Join(dir, "settings.json"), ReceiptPath: filepath.Join(dir, claudeReceiptName), GatewayURL: gatewayURL,
	}
	original, exists, err := readPrivateRegular(plan.SettingsPath, "settings.json")
	if err != nil {
		return nil, err
	}
	settings, err := parseOrderedObject(original)
	if err != nil {
		return nil, fmt.Errorf("settings.json is not valid: %w", err)
	}
	settingsEnv, hadEnv, err := envOf(settings)
	if err != nil {
		return nil, err
	}
	if err := checkClaudeSettingsConflicts(settings, settingsEnv); err != nil {
		return nil, err
	}
	currentURL, _ := jsonString(settingsEnv.values[claudeBaseURLKey])
	currentToken, _ := jsonString(settingsEnv.values[claudeAuthTokenKey])
	recordData, recorded, err := readPrivateRegular(plan.ReceiptPath, "connection record")
	if err != nil {
		return nil, err
	}
	var existing claudeReceiptFile
	if recorded {
		// The record, not just the settings, must name this gateway and token.
		if json.Unmarshal(recordData, &existing) != nil || existing.Version != 1 {
			return nil, fmt.Errorf("%w: the connection record is not valid", ErrClaudeSettingsChanged)
		}
		applied := currentURL == existing.GatewayURL && tokenDigest(currentToken) == existing.TokenSHA256
		if !applied && !existing.matchesOriginal(original, exists) {
			return nil, fmt.Errorf("%w: the gateway settings no longer match their connection record", ErrClaudeSettingsChanged)
		}
		if existing.GatewayURL != gatewayURL || existing.TokenSHA256 != tokenDigest(proxyToken) {
			return nil, fmt.Errorf("%w: an earlier term-llm gateway connection is recorded; undo it first", ErrClaudeSettingsConflict)
		}
		if applied {
			plan.AlreadyApplied = true
			return plan, nil
		}
		// Otherwise an earlier Apply recorded its intent and stopped before
		// settings.json changed; the same change is rebuilt and verified below.
	}
	backupPath := filepath.Join(dir, claudeBackupName)
	if !recorded {
		// The fixed backup name must not replace a file that is not ours.
		if _, err := os.Lstat(backupPath); !os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s already exists and is not recorded as ours", ErrClaudeSettingsConflict, backupPath)
		}
	}
	// An unrecorded base URL or bearer is someone else's routing or credential
	// choice. It is never replaced, only adopted when it already matches.
	for key, want := range map[string]string{claudeBaseURLKey: gatewayURL, claudeAuthTokenKey: proxyToken} {
		if raw, present := settingsEnv.values[key]; present {
			if value, isString := jsonString(raw); !isString || value != want {
				return nil, conflict("settings.json env", key)
			}
		}
	}
	record := claudeReceiptFile{
		Version: 1, GatewayURL: gatewayURL, TokenSHA256: tokenDigest(proxyToken), CreatedSettings: !exists, CreatedEnv: !hadEnv,
		PreviousBaseURL: settingsEnv.values[claudeBaseURLKey], PreviousToken: settingsEnv.values[claudeAuthTokenKey],
		OriginalSHA256: tokenDigest(string(original)),
	}
	urlJSON, _ := json.Marshal(gatewayURL)
	tokenJSON, _ := json.Marshal(proxyToken)
	settingsEnv.set(claudeBaseURLKey, urlJSON)
	settingsEnv.set(claudeAuthTokenKey, tokenJSON)
	settings.set("env", settingsEnv.compact())
	plan.original, plan.updated, plan.CreatesSettings = original, settings.document(), !exists
	record.UpdatedSHA256 = tokenDigest(string(plan.updated))
	if exists {
		plan.BackupPath = backupPath
	}
	if recorded {
		if existing.UpdatedSHA256 != record.UpdatedSHA256 {
			return nil, fmt.Errorf("%w: the recorded change cannot be reproduced", ErrClaudeSettingsChanged)
		}
		plan.receipt, plan.resumed = recordData, true
		return plan, nil
	}
	if plan.receipt, err = json.MarshalIndent(record, "", "  "); err != nil {
		return nil, err
	}
	return plan, nil
}

// Summary describes the planned change without any secret, for display before
// the user approves Apply.
func (p *ClaudeSettingsPlan) Summary() string {
	if p.AlreadyApplied {
		return fmt.Sprintf("%s already points Claude Code at %s; nothing to change.", p.SettingsPath, p.GatewayURL)
	}
	action, backup := "Update", fmt.Sprintf("A private backup is kept at %s.", p.BackupPath)
	if p.CreatesSettings {
		action, backup = "Create", "No settings file exists yet, so there is nothing to back up."
	}
	return fmt.Sprintf("%s %s: set env.%s to %s and env.%s to this machine's scoped proxy token (not a Claude credential). "+
		"All other settings are preserved; the file and its directory are made private. %s Undo is recorded in %s.",
		action, p.SettingsPath, claudeBaseURLKey, p.GatewayURL, claudeAuthTokenKey, backup, p.ReceiptPath)
}

func (p *ClaudeSettingsPlan) result() ClaudeSettingsReceipt {
	return ClaudeSettingsReceipt{
		SettingsPath: p.SettingsPath, BackupPath: p.BackupPath, ReceiptPath: p.ReceiptPath, GatewayURL: p.GatewayURL, CreatedSettings: p.CreatesSettings,
	}
}

// ownBackup reports whether the backup file is absent, or is ours: a regular
// file holding exactly the recorded original settings.
func ownBackup(path, originalSHA256 string) (present, own bool) {
	data, exists, err := readPrivateRegular(path, "settings backup")
	if err != nil {
		return true, false
	}
	return exists, exists && originalSHA256 != "" && tokenDigest(string(data)) == originalSHA256
}

// Apply writes the planned change. The caller must have the user's explicit
// approval. It refuses when settings.json changed since the plan was made.
// The undo record is written first as a statement of intent, then the backup,
// then settings.json. After any write error the record and backup are left in
// place, because a failed write may still have landed: planning again resumes
// the same change, and UndoClaudeSettings clears it.
func (p *ClaudeSettingsPlan) Apply() (ClaudeSettingsReceipt, error) {
	if p.AlreadyApplied {
		return p.result(), nil
	}
	dir := filepath.Dir(p.SettingsPath)
	if _, err := claudeSettingsDir(filepath.Dir(dir)); err != nil {
		return ClaudeSettingsReceipt{}, err
	}
	current, exists, err := readPrivateRegular(p.SettingsPath, "settings.json")
	if err != nil {
		return ClaudeSettingsReceipt{}, err
	}
	if exists == p.CreatesSettings || !bytes.Equal(current, p.original) {
		return ClaudeSettingsReceipt{}, ErrClaudeSettingsChanged
	}
	record, recorded, err := readPrivateRegular(p.ReceiptPath, "connection record")
	if err != nil || recorded != p.resumed || recorded && !bytes.Equal(record, p.receipt) {
		return ClaudeSettingsReceipt{}, ErrClaudeSettingsChanged
	}
	backupPresent, backupOwn := ownBackup(filepath.Join(dir, claudeBackupName), tokenDigest(string(p.original)))
	if backupPresent && !(p.resumed && backupOwn && p.BackupPath != "") {
		return ClaudeSettingsReceipt{}, fmt.Errorf("%w: an unrecognised settings backup is present", ErrClaudeSettingsChanged)
	}
	if err := fileprivacy.MkdirAll(dir); err != nil {
		return ClaudeSettingsReceipt{}, fmt.Errorf("create ~/.claude: %w", err)
	}
	if err := fileprivacy.ChmodDir(dir, fileprivacy.DirMode); err != nil {
		return ClaudeSettingsReceipt{}, fmt.Errorf("make ~/.claude private: %w", err)
	}
	if !p.resumed {
		if err := writeClaudeFile(p.ReceiptPath, p.receipt); err != nil {
			return ClaudeSettingsReceipt{}, fmt.Errorf("write connection record: %w", err)
		}
		p.resumed = true
	}
	if p.BackupPath != "" && !backupPresent {
		if err := writeClaudeFile(p.BackupPath, p.original); err != nil {
			return ClaudeSettingsReceipt{}, fmt.Errorf("write settings backup: %w", err)
		}
	}
	if err := writeClaudeFile(p.SettingsPath, p.updated); err != nil {
		return ClaudeSettingsReceipt{}, fmt.Errorf("write settings.json: %w", err)
	}
	p.AlreadyApplied = true
	return p.result(), nil
}

// restoreClaudeGatewayKeys puts the two gateway keys back to their recorded
// earlier values and drops the env object or file only if Apply created them.
func restoreClaudeGatewayKeys(settings, env *orderedObject, record claudeReceiptFile, settingsPath string) error {
	for key, previous := range map[string]json.RawMessage{claudeBaseURLKey: record.PreviousBaseURL, claudeAuthTokenKey: record.PreviousToken} {
		if len(previous) == 0 || string(previous) == "null" {
			env.remove(key)
		} else {
			env.set(key, previous)
		}
	}
	if len(env.keys) == 0 && record.CreatedEnv {
		settings.remove("env")
	} else {
		settings.set("env", env.compact())
	}
	if len(settings.keys) == 0 && record.CreatedSettings {
		return os.Remove(settingsPath)
	}
	return writeClaudeFile(settingsPath, settings.document())
}

// UndoClaudeSettings removes a connection made by Apply. It restores only the
// two gateway keys to their earlier values and keeps every later, unrelated
// edit; the backup is never copied back over the file. If either gateway key
// no longer holds what Apply wrote, it returns ErrClaudeSettingsChanged and
// modifies nothing. An Apply that recorded its intent but never changed
// settings.json is cleared without touching settings. On success the undo
// record and the backup it can verify as its own are removed.
func UndoClaudeSettings(home string) error {
	dir, err := claudeSettingsDir(home)
	if err != nil {
		return err
	}
	receiptPath, settingsPath := filepath.Join(dir, claudeReceiptName), filepath.Join(dir, "settings.json")
	data, recorded, err := readPrivateRegular(receiptPath, "connection record")
	if err != nil {
		return err
	}
	var record claudeReceiptFile
	if !recorded {
		return ErrClaudeSettingsNotApplied
	}
	if json.Unmarshal(data, &record) != nil || record.Version != 1 {
		return errors.New("connection record is not valid; nothing was modified")
	}
	current, exists, err := readPrivateRegular(settingsPath, "settings.json")
	if err != nil {
		return err
	}
	settings, err := parseOrderedObject(current)
	if err != nil {
		return fmt.Errorf("settings.json is not valid: %w", err)
	}
	env, _, err := envOf(settings)
	if err != nil {
		return err
	}
	currentURL, _ := jsonString(env.values[claudeBaseURLKey])
	currentToken, isToken := jsonString(env.values[claudeAuthTokenKey])
	switch {
	case exists && currentURL == record.GatewayURL && isToken && tokenDigest(currentToken) == record.TokenSHA256:
		if err := restoreClaudeGatewayKeys(settings, env, record, settingsPath); err != nil {
			return fmt.Errorf("restore settings.json: %w", err)
		}
	case record.matchesOriginal(current, exists):
		// Intent was recorded but settings.json never changed.
	default:
		return fmt.Errorf("%w: the gateway settings were edited after they were applied", ErrClaudeSettingsChanged)
	}
	// Records written before hashes existed cannot verify a backup; theirs is
	// removed as it always was. Otherwise only a verified own backup is removed.
	backupPath := filepath.Join(dir, claudeBackupName)
	if present, own := ownBackup(backupPath, record.OriginalSHA256); present && (own || record.OriginalSHA256 == "") {
		if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", claudeBackupName, err)
		}
	}
	if err := os.Remove(receiptPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", claudeReceiptName, err)
	}
	return nil
}

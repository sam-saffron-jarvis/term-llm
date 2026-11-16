package proxyconnect

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/term-llm/term-llm/internal/filelock"
	"github.com/term-llm/term-llm/internal/fileprivacy"
)

const connectorPendingName = "term-llm-connect-pending.json"
const connectorStateName = "term-llm-connector.json"

type pendingConnect struct {
	Version int          `json:"version"`
	URL     string       `json:"url"`
	Start   connectStart `json:"request"`
}
type connectorState struct {
	Version    int    `json:"version"`
	URL        string `json:"url"`
	Account    string `json:"account"`
	CallerID   string `json:"caller_id,omitempty"`
	GatewayURL string `json:"gateway_url"`
}

func connectRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("Could not create private pairing capability")
	}
	return hex.EncodeToString(b), nil
}
func readConnectorJSON(path string, v any) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || !ownedByCurrentUser(info) || fileprivacy.CheckFile(info) != nil || info.Size() > 64<<10 {
		return false, errors.New("Connector state must be a private owner-only regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, errors.New("Could not read private connector state")
	}
	if json.Unmarshal(raw, v) != nil {
		return false, errors.New("Invalid private connector state; inspect it before enrolling again")
	}
	return true, nil
}
func writeConnectorJSON(path string, v any) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || fileprivacy.CheckFile(info) != nil {
			return errors.New("Unsafe connector state file")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("Could not inspect connector state")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return errors.New("Could not prepare private connector state")
	}
	if fileprivacy.WriteFileAtomic(path, raw) != nil {
		return errors.New("Could not durably save connector state")
	}
	return nil
}
func connectorLock(home string) (string, func() error, error) {
	dir, err := claudeSettingsDir(home)
	if err != nil {
		return "", nil, err
	}
	if err = fileprivacy.MkdirAll(dir); err != nil {
		return "", nil, errors.New("Could not create private Claude connector directory")
	}
	if err = fileprivacy.ChmodDir(dir, fileprivacy.DirMode); err != nil {
		return "", nil, errors.New("Could not protect Claude connector directory")
	}
	lock := filepath.Join(dir, ".term-llm-connect.lock")
	if info, e := os.Lstat(lock); e == nil {
		if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || fileprivacy.CheckFile(info) != nil {
			return "", nil, errors.New("Unsafe connector lock file")
		}
	} else if !os.IsNotExist(e) {
		return "", nil, errors.New("Could not inspect connector lock")
	}
	unlock, err := filelock.TryLock(lock)
	if err != nil {
		return "", nil, errors.New("Another Claude connector operation is running")
	}
	return dir, unlock, nil
}
func loadPending(dir string) (pendingConnect, bool, error) {
	var p pendingConnect
	found, err := readConnectorJSON(filepath.Join(dir, connectorPendingName), &p)
	if err != nil || !found {
		return p, found, err
	}
	root, e := normalizedConnectURL(p.URL)
	if e != nil || root != p.URL || p.Version != 1 || !hexLength(p.Start.ID, 16) || !hexLength(p.Start.Secret, 32) || !validConnectAccount(p.Start.Account) || (p.Start.Action != "auto" && p.Start.Action != "reconnect") {
		return p, true, errors.New("Invalid pending connector receipt; no new request was created")
	}
	return p, true, nil
}
func hexLength(s string, n int) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == n }
func loadConnectorState(dir string) (connectorState, bool, error) {
	var state connectorState
	ok, err := readConnectorJSON(filepath.Join(dir, connectorStateName), &state)
	if err != nil || !ok {
		return state, ok, err
	}
	root, e := normalizedConnectURL(state.URL)
	if e != nil || state.Version != 1 || root != state.URL || !validConnectAccount(state.Account) || state.GatewayURL != state.URL+"/"+state.Account+"/" {
		return state, true, errors.New("Invalid saved Claude connector identity")
	}
	return state, true, nil
}

package proxyconnect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

func (r *connectorRun) existingToken() (string, error) {
	raw, exists, err := readPrivateRegular(filepath.Join(r.dir, "settings.json"), "settings.json")
	if err != nil || !exists {
		return "", errors.New("Recorded Claude connection has no readable settings; inspect before reconnecting")
	}
	settings, err := parseOrderedObject(raw)
	if err != nil {
		return "", errors.New("Claude settings changed; inspect before reconnecting")
	}
	env, _, err := envOf(settings)
	if err != nil {
		return "", err
	}
	token, ok := jsonString(env.values[claudeAuthTokenKey])
	if !ok {
		return "", ErrClaudeSettingsChanged
	}
	plan, err := PlanClaudeSettings(r.opts.Home, r.prior.GatewayURL, token, r.opts.Env)
	if err != nil {
		return "", err
	}
	if !plan.AlreadyApplied {
		return "", ErrClaudeSettingsChanged
	}
	return token, nil
}
func (r *connectorRun) approveLocalSettings(out ConnectRequest) error {
	if out.Status != "approved" {
		return errors.New("An exact browser approval is required before Claude sign-in or configuration")
	}
	if out.ResolvedAction != "add" && out.ResolvedAction != "device" && out.ResolvedAction != "reconnect" {
		return errors.New("Browser approval did not resolve a supported Claude operation")
	}
	if r.hasPrior && r.pending.Start.ClientName == "" {
		_, err := r.existingToken()
		return err
	}
	// Planning has no side effects; the placeholder is never installed or sent.
	plan, err := PlanClaudeSettings(r.opts.Home, r.pending.URL+out.Prefix, "sk-tlp-"+strings.Repeat("0", 64), r.opts.Env)
	if err != nil {
		return err
	}
	ok, err := r.confirm(plan.Summary() + " Apply this Claude-only change after the proxy commits?")
	if err != nil {
		return err
	}
	if !ok {
		return errConnectCanceled
	}
	r.settingsApproved = true
	return nil
}
func (r *connectorRun) subscriptionToken(ctx context.Context, out ConnectRequest) (string, error) {
	if out.ResolvedAction == "device" {
		fmt.Fprintln(r.opts.Out, "The approved Claude account is already connected. Pairing this scoped client only; no Claude sign-in is needed.")
		return "", nil
	}
	if out.ResolvedAction != "add" && out.ResolvedAction != "reconnect" {
		return "", errors.New("Subscription sign-in is not approved for this operation")
	}
	if out.Expires <= time.Now().Unix() {
		return "", errors.New("Browser approval expired before sign-in; no native command was run")
	}
	signIn, cancel := context.WithDeadline(ctx, time.Unix(out.Expires, 0))
	defer cancel()
	var token string
	var err error
	if r.opts.PasteToken {
		token, err = r.readSubscriptionToken()
	} else {
		ok, e := r.confirm("Run official claude setup-token on this trusted enrollment machine? Complete the visible browser sign-in. Its token briefly resides here before private upload; command output is hidden to protect it.")
		if e != nil {
			return "", e
		}
		if !ok {
			return "", errConnectCanceled
		}
		fmt.Fprintln(r.opts.Out, "Running official Claude subscription sign-in. Complete its browser window. If it cannot open a browser or needs hidden terminal prompts, cancel and resume with --paste-token; no output will be echoed.")
		nativeContext, stop := signal.NotifyContext(signIn, syscall.SIGINT, syscall.SIGTERM)
		token, err = r.opts.AcquireToken(nativeContext, r.opts.ClaudeCommand, r.opts.In)
		stop()
	}
	if err != nil {
		return "", errors.New("Claude token handoff did not complete. No token output was shown; resume with --paste-token if needed")
	}
	if len(token) < 20 || len(token) > 16384 || strings.ContainsAny(token, " \t\r\n\x00") || strings.HasPrefix(token, "sk-ant-api") || strings.HasPrefix(token, "sk-tlp-") || strings.HasPrefix(token, "admin_") {
		return "", errors.New("Use one Claude subscription OAuth setup token, not an API key, proxy token or administrator credential")
	}
	return token, nil
}
func (r *connectorRun) readSubscriptionToken() (string, error) {
	if r.opts.ReadToken != nil {
		return r.opts.ReadToken()
	}
	file, ok := r.opts.In.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", errors.New("Token paste requires a terminal with echo disabled")
	}
	// Raw mode makes Ctrl-C an input cancellation, so terminal state is restored
	// by this defer rather than leaving echo disabled when the process exits.
	previous, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return "", errors.New("Could not protect terminal token input")
	}
	defer term.Restore(int(file.Fd()), previous)
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{file, r.opts.Out}, "")
	value, err := terminal.ReadPassword("Claude subscription setup token (hidden): ")
	if err != nil {
		return "", errors.New("Hidden subscription-token input was canceled")
	}
	return strings.TrimSpace(value), nil
}
func (r *connectorRun) applyResult(ctx context.Context, out ConnectRequest) error {
	if err := r.checkResponse(out); err != nil {
		return err
	}
	if out.Status != "complete" && out.Status != "acknowledged" {
		return errors.New("Connection has no completed receipt; resume it rather than enrolling another client")
	}
	result := out.Result
	if result == nil || result.GatewayURL != r.pending.URL+"/"+r.pending.Start.Account+"/" {
		return errors.New("Connection receipt has no exact gateway result; nothing was installed")
	}
	if result.Token == "" {
		if err := r.recoverWithoutToken(result); err != nil {
			return err
		}
	} else if err := r.installResult(result); err != nil {
		return err
	}
	if err := r.client.finish(ctx, "ack", r.pending.Start.ID, r.pending.Start.Secret); err != nil {
		var httpErr *connectHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != 410 {
			return errors.New("Claude settings are saved, but receipt acknowledgement is uncertain; rerun this command to finish the same connection")
		}
	}
	if err := os.Remove(filepath.Join(r.dir, connectorPendingName)); err != nil {
		return errors.New("Claude is configured; could not remove its completed local pending receipt")
	}
	fmt.Fprintln(r.opts.Out, "Claude connected. Run plain claude. Restart any already-running Claude process to use the configured client identity. Undo: term-llm proxy disconnect claude.")
	return nil
}
func (r *connectorRun) recoverWithoutToken(result *ConnectResult) error {
	if !r.hasPrior || r.prior.URL != r.pending.URL || r.prior.Account != r.pending.Start.Account {
		return errors.New("The receipt no longer contains a client token. Inspect/revoke its client in the proxy browser; no replacement was minted")
	}
	if result.CallerID != "" && result.CallerID != r.prior.CallerID {
		return errors.New("The completed receipt belongs to another client; inspect it in the proxy browser")
	}
	if _, err := r.existingToken(); err != nil {
		return err
	}
	return nil
}
func (r *connectorRun) installResult(result *ConnectResult) error {
	if !strings.HasPrefix(result.Token, "sk-tlp-") || !hexLength(strings.TrimPrefix(result.Token, "sk-tlp-"), 32) || result.CallerID == "" {
		return errors.New("Receipt did not contain a scoped proxy client credential; nothing was installed")
	}
	plan, err := PlanClaudeSettings(r.opts.Home, result.GatewayURL, result.Token, r.opts.Env)
	if err != nil {
		return err
	}
	if !r.settingsApproved && !plan.AlreadyApplied {
		ok, e := r.confirm(plan.Summary() + " Apply this Claude-only change?")
		if e != nil {
			return e
		}
		if !ok {
			return errConnectCanceled
		}
	}
	if _, err = plan.Apply(); err != nil {
		return err
	}
	state := connectorState{Version: 1, URL: r.pending.URL, Account: r.pending.Start.Account, CallerID: result.CallerID, GatewayURL: result.GatewayURL}
	if err = writeConnectorJSON(filepath.Join(r.dir, connectorStateName), state); err != nil {
		return err
	}
	r.prior, r.hasPrior = state, true
	return nil
}

// Disconnect restores only owned Claude gateway keys. It does not alter global
// provider config or silently revoke server-side credentials shared elsewhere.
func Disconnect(opts Options) error {
	r, err := newConnectorRun(opts)
	if err != nil {
		return err
	}
	dir, err := claudeSettingsDir(r.opts.Home)
	if err != nil {
		return err
	}
	if _, pending, e := loadPending(dir); e != nil {
		return e
	} else if pending {
		return errors.New("Finish or explicitly cancel the pending Claude connection before undo")
	}
	ok, err := r.confirm("Undo only this user's Claude gateway settings, preserving other settings? Earlier direct Claude authentication may resume. Revoke this client's credential in proxy Clients to disable other copies.")
	if err != nil {
		return err
	}
	if !ok {
		return errConnectCanceled
	}
	dir, unlock, err := connectorLock(r.opts.Home)
	if err != nil {
		return err
	}
	defer unlock()
	if err = UndoClaudeSettings(r.opts.Home); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(dir, connectorStateName)); err != nil && !os.IsNotExist(err) {
		return errors.New("Claude settings restored; could not remove connector metadata")
	}
	fmt.Fprintln(r.opts.Out, "Claude gateway settings restored. Other settings were preserved. Revoke the scoped client in the proxy browser if no longer needed.")
	return nil
}

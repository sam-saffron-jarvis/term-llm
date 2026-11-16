package proxyconnect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/oauth"
)

var connectAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func validConnectAccount(s string) bool { return connectAccountPattern.MatchString(s) }

var errConnectCanceled = errors.New("Claude connection canceled; no enrollment was replayed")

// Options configure one standalone Claude enrollment. Injectable operations are
// for mechanical tests; production callers use the ordinary terminal and browser.
type Options struct {
	URL, Account, ClientName, Home, ClaudeCommand string
	Reconnect, PasteToken, NoBrowser, Cancel      bool
	In                                            io.Reader
	Out                                           io.Writer
	Env                                           []string
	HTTPClient                                    *http.Client
	Confirm                                       func(string) (bool, error)
	ReadToken                                     func() (string, error)
	OpenBrowser                                   func(string) error
	AcquireToken                                  func(context.Context, string, io.Reader) (string, error)
	PollInterval                                  time.Duration
}
type connectorRun struct {
	opts             Options
	input            *bufio.Reader
	dir              string
	client           *connectClient
	pending          pendingConnect
	prior            connectorState
	hasPrior         bool
	settingsApproved bool
}

// Run pairs or explicitly reconnects Claude without writing term-llm's global
// proxy block, shell configuration, project configuration or tool permissions.
func Run(ctx context.Context, opts Options) error {
	run, err := newConnectorRun(opts)
	if err != nil {
		return err
	}
	ok, err := run.confirm("Pair standalone Claude with this proxy and create private Claude-only connector state? No other providers or shell settings will change.")
	if err != nil {
		return err
	}
	if !ok {
		return errConnectCanceled
	}
	dir, unlock, err := connectorLock(run.opts.Home)
	if err != nil {
		return err
	}
	defer unlock()
	run.dir = dir
	if err = run.loadOrCreatePending(); err != nil {
		return err
	}
	if run.opts.Cancel {
		return run.cancel(ctx)
	}
	if run.pending.Start.ID == "" {
		fmt.Fprintln(run.opts.Out, "Claude is already configured. Run plain claude; use --reconnect only to replace the subscription token.")
		return nil
	}
	run.client = newConnectClient(run.pending.URL, run.opts.HTTPClient)
	response, err := run.client.start(ctx, run.pending.Start)
	if err != nil {
		response, err = run.client.status(ctx, run.pending.Start.ID, run.pending.Start.Secret)
		if err != nil {
			return err
		}
	}
	if err = run.checkResponse(response); err != nil {
		return err
	}
	response, err = run.waitApproval(ctx, response)
	if err != nil {
		return err
	}
	if response.Status == "complete" || response.Status == "acknowledged" {
		return run.applyResult(ctx, response)
	}
	if err = run.approveLocalSettings(response); err != nil {
		return err
	}
	token, err := run.subscriptionToken(ctx, response)
	if err != nil {
		return err
	}
	if response.Expires <= time.Now().Unix() {
		token = ""
		return errors.New("Approval expired before upload; any newly issued setup token may need revoking with Claude. No new enrollment was replayed")
	}
	response, err = run.client.commit(ctx, run.pending.Start.ID, run.pending.Start.Secret, token)
	token = ""
	if err != nil {
		// The existing receipt, not another commit/new client, decides uncertain effects.
		recovered, recoveryErr := run.client.status(ctx, run.pending.Start.ID, run.pending.Start.Secret)
		if recoveryErr != nil || recovered.Status != "complete" {
			return errors.New("Connection outcome needs inspection; rerun the same command to resume its saved receipt, not a new enrollment")
		}
		response = recovered
	}
	return run.applyResult(ctx, response)
}
func newConnectorRun(opts Options) (*connectorRun, error) {
	if opts.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.New("Could not locate selected user home")
		}
		opts.Home = home
	}
	opts.Home = filepath.Clean(opts.Home)
	if _, err := claudeSettingsDir(opts.Home); err != nil {
		return nil, err
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stderr
	}
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	if opts.ClaudeCommand == "" {
		opts.ClaudeCommand = "claude"
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = time.Second
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = oauth.OpenBrowser
	}
	if opts.AcquireToken == nil {
		opts.AcquireToken = AcquireClaudeToken
	}
	return &connectorRun{opts: opts, input: bufio.NewReader(opts.In)}, nil
}
func (r *connectorRun) confirm(prompt string) (bool, error) {
	if r.opts.Confirm != nil {
		return r.opts.Confirm(prompt)
	}
	fmt.Fprint(r.opts.Out, prompt+" [y/N] ")
	line, err := r.readLine()
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}
func (r *connectorRun) readLine() (string, error) {
	raw, err := r.input.ReadSlice('\n')
	if len(raw) > 2048 || errors.Is(err, bufio.ErrBufferFull) {
		return "", errors.New("Terminal input is too long")
	}
	if err != nil {
		return "", errors.New("Interactive input ended; no new enrollment was created")
	}
	return string(raw), nil
}
func (r *connectorRun) loadOrCreatePending() error {
	prior, found, err := loadConnectorState(r.dir)
	if err != nil {
		return err
	}
	r.prior, r.hasPrior = prior, found
	p, pending, err := loadPending(r.dir)
	if err != nil {
		return err
	}
	if pending {
		if r.opts.URL != "" {
			root, e := normalizedConnectURL(r.opts.URL)
			if e != nil || root != p.URL {
				return errors.New("A saved connection is for another proxy; cancel or finish it explicitly first")
			}
		}
		if r.opts.Account != "" && r.opts.Account != p.Start.Account {
			return errors.New("A saved connection is for another Claude account; cancel or finish it first")
		}
		r.pending = p
		return nil
	}
	if r.opts.Cancel {
		return nil
	}
	root, err := r.chooseURL()
	if err != nil {
		return err
	}
	account := r.opts.Account
	if account == "" {
		account = "claude"
		if found {
			account = prior.Account
		}
	}
	if !validConnectAccount(account) {
		return errors.New("Claude account must be a short slug")
	}
	if found && (prior.URL != root || prior.Account != account) {
		return errors.New("Claude already has a different connector; disconnect it explicitly before pairing another")
	}
	if found && !r.opts.Reconnect {
		_, err := r.existingToken()
		return err
	}
	name := strings.TrimSpace(r.opts.ClientName)
	if found && r.opts.Reconnect {
		name = ""
	} else if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "Claude computer"
		}
	}
	if len(name) > 80 || strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("Client name must be at most 80 bytes without control characters")
	}
	id, err := connectRandomHex(16)
	if err != nil {
		return err
	}
	secret, err := connectRandomHex(32)
	if err != nil {
		return err
	}
	action := "auto"
	if r.opts.Reconnect {
		action = "reconnect"
	}
	r.pending = pendingConnect{Version: 1, URL: root, Start: connectStart{ID: id, Secret: secret, Account: account, ClientName: name, Action: action}}
	return writeConnectorJSON(filepath.Join(r.dir, connectorPendingName), r.pending)
}
func (r *connectorRun) chooseURL() (string, error) {
	raw := r.opts.URL
	if raw == "" && r.hasPrior {
		raw = r.prior.URL
	}
	if raw == "" {
		fmt.Fprint(r.opts.Out, "Proxy URL [http://localhost:8082]: ")
		line, err := r.readLine()
		if err != nil {
			return "", err
		}
		raw = strings.TrimSpace(line)
		if raw == "" {
			raw = "http://localhost:8082"
		}
	}
	return normalizedConnectURL(raw)
}
func (r *connectorRun) checkResponse(out ConnectRequest) error {
	if out.ID != r.pending.Start.ID || out.Origin != r.pending.URL || out.Account != r.pending.Start.Account || out.ClientName != r.pending.Start.ClientName || out.Action != r.pending.Start.Action || out.Route != r.pending.Start.Account || out.Prefix != "/"+r.pending.Start.Account+"/" {
		return errors.New("Connection receipt does not match the exact requesting origin/account; nothing was installed")
	}
	if out.ResolvedAction != "" && ((r.pending.Start.Action == "reconnect" && out.ResolvedAction != "reconnect") || (r.pending.Start.Action == "auto" && out.ResolvedAction != "add" && out.ResolvedAction != "device")) {
		return errors.New("Browser resolution does not match the requested Claude action")
	}
	return nil
}
func (r *connectorRun) waitApproval(ctx context.Context, out ConnectRequest) (ConnectRequest, error) {
	if out.Status != "pending" {
		return out, nil
	}
	if !hexLength(out.Code, 4) {
		return out, errors.New("Invalid approval code")
	}
	values := url.Values{"connect": {out.ID}}
	link := r.pending.URL + "/_proxy/?" + values.Encode()
	fmt.Fprintf(r.opts.Out, "Approve this Claude connection in your proxy browser. Type code %s from YOUR terminal in the browser.\n%s\n", out.Code, link)
	if !r.opts.NoBrowser {
		if r.opts.OpenBrowser(link) != nil {
			fmt.Fprintln(r.opts.Out, "Open the link manually; browser launch was unavailable.")
		}
	}
	wait, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	for out.Status == "pending" {
		timer := time.NewTimer(r.opts.PollInterval)
		select {
		case <-wait.Done():
			timer.Stop()
			return out, errors.New("Approval wait ended; the saved request can be resumed or explicitly canceled")
		case <-timer.C:
		}
		next, err := r.client.status(wait, r.pending.Start.ID, r.pending.Start.Secret)
		if err != nil {
			return out, err
		}
		if err = r.checkResponse(next); err != nil {
			return out, err
		}
		out = next
	}
	if out.Status != "approved" && out.Status != "complete" && out.Status != "acknowledged" {
		return out, errors.New("Connection was canceled, expired or refused; inspect its saved receipt before another enrollment")
	}
	return out, nil
}
func (r *connectorRun) cancel(ctx context.Context) error {
	if r.pending.Start.ID == "" {
		fmt.Fprintln(r.opts.Out, "No pending Claude enrollment.")
		return nil
	}
	r.client = newConnectClient(r.pending.URL, r.opts.HTTPClient)
	status, err := r.client.status(ctx, r.pending.Start.ID, r.pending.Start.Secret)
	if err != nil {
		return err
	}
	if status.Status == "complete" || status.Status == "acknowledged" {
		return errors.New("This connection committed; resume it to recover settings or revoke its client in the proxy browser")
	}
	if err = r.client.finish(ctx, "cancel", r.pending.Start.ID, r.pending.Start.Secret); err != nil {
		return err
	}
	if os.Remove(filepath.Join(r.dir, connectorPendingName)) != nil {
		return errors.New("Canceled remotely; could not remove local pending receipt")
	}
	fmt.Fprintln(r.opts.Out, "Pending Claude enrollment canceled.")
	return nil
}

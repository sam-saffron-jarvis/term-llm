// Package agentmailbox implements the experimental, process-local sibling mailbox for immediate children of one delegated run.
package agentmailbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	SendToolName = "message_agent"
	WaitToolName = "wait_message"
	enabledEnv   = "TERM_LLM_EXPERIMENTAL_AGENT_MAILBOX"
	addressRule  = `^[a-zA-Z][a-zA-Z0-9_.-]{0,31}$`
	maxTextBytes = 8 << 10
	maxQueued    = 16
	maxSiblings  = 32
	maxWait      = 300
)

var addressPattern = regexp.MustCompile(addressRule)

func enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(enabledEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func IsTool(name string) bool { return name == SendToolName || name == WaitToolName }

func SpawnSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": addressRule, "description": "Experimental sibling address. Requires TERM_LLM_EXPERIMENTAL_AGENT_MAILBOX=1 on the host. Tell each sibling the addresses it should use."}
}

// Inbox is one generation's unforgeable, host-bound mailbox. Mail belongs to
// the recipient inbox; ending the sender does not change already queued mail.
type Inbox struct {
	done                <-chan struct{}
	parent, id, address string
	name                string
	generation          int64
	changed             chan struct{}
	queue               []message
}

type message struct {
	From       string    `json:"from"`
	Agent      string    `json:"from_agent"`
	ID         string    `json:"from_agent_id"`
	Generation int64     `json:"from_generation"`
	Text       string    `json:"text"`
	SentAt     time.Time `json:"sent_at"`
}

type inboxKey struct{}

var mailboxes = struct {
	sync.Mutex
	parents map[string]map[string]*Inbox
}{parents: make(map[string]map[string]*Inbox)}

// Admit binds only this spawn. Inherited enrollment is masked, even when the
// caller is itself an enrolled child. Continuations supply no new address.
func Admit(ctx context.Context, runner any, parent, id, name string, generation int64, address *string) (context.Context, error) {
	var inbox *Inbox
	if address != nil {
		var err error
		inbox, err = open(ctx, runner, parent, id, name, generation, *address)
		if err != nil {
			return ctx, err
		}
	}
	return context.WithValue(ctx, inboxKey{}, inbox), nil
}

func open(ctx context.Context, runner any, parent, id, name string, generation int64, address string) (*Inbox, error) {
	if !enabled() {
		return nil, fmt.Errorf("sibling mailbox is disabled; set %s=1 on the host", enabledEnv)
	}
	if capable, ok := runner.(interface{ SupportsAgentMailbox() bool }); !ok || !capable.SupportsAgentMailbox() {
		return nil, errors.New("this spawn runner does not support the sibling mailbox")
	}
	if parent == "" || !addressPattern.MatchString(address) {
		return nil, errors.New("mailbox requires a parent session and an address matching " + addressRule)
	}
	mailboxes.Lock()
	defer mailboxes.Unlock()
	siblings := mailboxes.parents[parent]
	if siblings[address] != nil {
		return nil, fmt.Errorf("mailbox address %q is already in use", address)
	}
	if len(siblings) >= maxSiblings {
		return nil, errors.New("sibling mailbox cohort is full")
	}
	if siblings == nil {
		siblings = make(map[string]*Inbox)
		mailboxes.parents[parent] = siblings
	}
	inbox := &Inbox{done: ctx.Done(), parent: parent, id: id, name: name, address: address, generation: generation, changed: make(chan struct{})}
	siblings[address] = inbox
	return inbox, nil
}

// Close ends this generation and discards its unread inbox.
func Close(ctx context.Context) {
	i, _ := ctx.Value(inboxKey{}).(*Inbox)
	if i == nil {
		return
	}
	mailboxes.Lock()
	defer mailboxes.Unlock()
	siblings := mailboxes.parents[i.parent]
	if siblings[i.address] != i {
		return
	}
	delete(siblings, i.address)
	if len(siblings) == 0 {
		delete(mailboxes.parents, i.parent)
	}
	i.queue = nil
	close(i.changed)
}

func (i *Inbox) liveLocked() bool {
	if i == nil || mailboxes.parents[i.parent][i.address] != i {
		return false
	}
	select {
	case <-i.done:
		return false
	default:
		return true
	}
}

func (i *Inbox) Available() bool {
	mailboxes.Lock()
	defer mailboxes.Unlock()
	return enabled() && i.liveLocked()
}

// ForChild returns the bound inbox only for the exact child and parent session.
func ForChild(ctx context.Context, id, parent string) *Inbox {
	i, _ := ctx.Value(inboxKey{}).(*Inbox)
	if i != nil && i.id == id && i.parent == parent && i.Available() {
		return i
	}
	return nil
}

var errNotLive = errors.New("not a live sibling mailbox participant")

func (i *Inbox) send(ctx context.Context, to, text string) error {
	mailboxes.Lock()
	defer mailboxes.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !enabled() || !i.liveLocked() {
		return errNotLive
	}
	peer := mailboxes.parents[i.parent][to]
	if peer == i || !peer.liveLocked() {
		return fmt.Errorf("no live sibling mailbox at %q", to)
	}
	if len(peer.queue) >= maxQueued {
		return errors.New("recipient mailbox is full")
	}
	peer.queue = append(peer.queue, message{i.address, i.name, i.id, i.generation, text, time.Now().UTC()})
	close(peer.changed)
	peer.changed = make(chan struct{})
	return nil
}

// poll claims one message or returns the current notification channel. The
// registry lock serializes the claim with closing or reusing the address.
func (i *Inbox) poll(ctx context.Context) (*message, <-chan struct{}, error) {
	mailboxes.Lock()
	defer mailboxes.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !enabled() || !i.liveLocked() {
		return nil, nil, errNotLive
	}
	if len(i.queue) == 0 {
		return nil, i.changed, nil
	}
	m := i.queue[0]
	i.queue[0] = message{}
	i.queue = i.queue[1:]
	return &m, nil, nil
}

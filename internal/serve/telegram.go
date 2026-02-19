package serve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm"
)

const telegramMaxMessageLen = 4000 // Telegram limit is 4096; leave margin

// TelegramPlatform implements Platform for the Telegram messaging platform.
type TelegramPlatform struct {
	cfg config.TelegramServeConfig
}

// NewTelegramPlatform creates a new TelegramPlatform with the given config.
func NewTelegramPlatform(cfg config.TelegramServeConfig) *TelegramPlatform {
	return &TelegramPlatform{cfg: cfg}
}

func (p *TelegramPlatform) Name() string { return "telegram" }

// NeedsSetup returns true when the bot token is missing.
func (p *TelegramPlatform) NeedsSetup() bool {
	return strings.TrimSpace(p.cfg.Token) == ""
}

// RunSetup runs an interactive wizard that collects and persists bot credentials.
func (p *TelegramPlatform) RunSetup() error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("Telegram Bot Setup")
	fmt.Println("==================")
	fmt.Println()
	fmt.Println("1. Open @BotFather on Telegram → /newbot → copy the token")
	fmt.Print("   Token: ")

	if !scanner.Scan() {
		return fmt.Errorf("no input received")
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		return fmt.Errorf("token is required")
	}

	fmt.Println()
	fmt.Println("2. Whitelist Telegram user ID(s) and/or @username(s):")
	fmt.Println("   - Send any message to your bot")
	fmt.Printf("   - Visit https://api.telegram.org/bot%s/getUpdates\n", token)
	fmt.Println("   - Find the numeric 'id' or 'username' under 'from'")
	fmt.Println("   - Mix numeric IDs and @usernames freely (e.g. 123456, @alice)")
	fmt.Print("   Allowed users (comma-separated, required): ")

	if !scanner.Scan() {
		return fmt.Errorf("no input received")
	}
	rawEntries := strings.TrimSpace(scanner.Text())
	if rawEntries == "" {
		return fmt.Errorf("at least one user ID or username is required")
	}

	var userIDs []int64
	var usernames []string
	for _, part := range strings.Split(rawEntries, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "@") {
			name := strings.TrimPrefix(part, "@")
			if name != "" {
				usernames = append(usernames, strings.ToLower(name))
			}
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid entry %q: must be a numeric ID or @username", part)
		}
		userIDs = append(userIDs, id)
	}
	if len(userIDs) == 0 && len(usernames) == 0 {
		return fmt.Errorf("at least one valid user ID or @username is required")
	}

	newCfg := config.TelegramServeConfig{
		Token:            token,
		AllowedUserIDs:   userIDs,
		AllowedUsernames: usernames,
		IdleTimeout:      p.cfg.IdleTimeout,
	}

	if err := config.SetServeTelegramConfig(newCfg); err != nil {
		return fmt.Errorf("save telegram config: %w", err)
	}

	// Update in-memory config so Run() can proceed immediately after setup.
	p.cfg = newCfg
	fmt.Println()
	fmt.Println("Telegram configuration saved.")
	return nil
}

// Run starts the Telegram bot loop, blocking until ctx is cancelled.
func (p *TelegramPlatform) Run(ctx context.Context, cfg *config.Config, settings Settings) error {
	token := strings.TrimSpace(p.cfg.Token)
	if token == "" {
		return fmt.Errorf("telegram bot token is not configured; run with --setup to configure")
	}

	if len(p.cfg.AllowedUserIDs) == 0 && len(p.cfg.AllowedUsernames) == 0 {
		log.Println("[telegram] warning: no allowed_user_ids or allowed_usernames configured; all messages will be rejected")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return fmt.Errorf("telegram connect: %w", err)
	}
	log.Printf("[telegram] authorised as @%s", bot.Self.UserName)

	// Resolve idle timeout: CLI flag takes priority, then per-platform config.
	idleTimeout := settings.IdleTimeout
	if idleTimeout <= 0 {
		if p.cfg.IdleTimeout > 0 {
			idleTimeout = time.Duration(p.cfg.IdleTimeout) * time.Minute
		} else {
			idleTimeout = 30 * time.Minute
		}
	}

	mgr := &telegramSessionMgr{
		sessions:         make(map[int64]*telegramSession),
		cfg:              cfg,
		settings:         settings,
		idleTimeout:      idleTimeout,
		allowedUserIDs:   buildAllowedSet(p.cfg.AllowedUserIDs),
		allowedUsernames: buildAllowedUsernameSet(p.cfg.AllowedUsernames),
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			if update.Message == nil {
				continue
			}
			go mgr.handleMessage(ctx, bot, update.Message)
		}
	}
}

func buildAllowedSet(ids []int64) map[int64]struct{} {
	m := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func buildAllowedUsernameSet(names []string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, name := range names {
		m[strings.ToLower(name)] = struct{}{}
	}
	return m
}

// telegramSession holds per-chat conversation state.
type telegramSession struct {
	mu           sync.Mutex
	engine       *llm.Engine
	history      []llm.Message
	lastActivity time.Time
}

// telegramSessionMgr manages per-chat sessions.
type telegramSessionMgr struct {
	mu               sync.Mutex
	sessions         map[int64]*telegramSession
	cfg              *config.Config
	settings         Settings
	idleTimeout      time.Duration
	allowedUserIDs   map[int64]struct{}
	allowedUsernames map[string]struct{}
}

func (m *telegramSessionMgr) isAllowed(userID int64, username string) bool {
	if len(m.allowedUserIDs) == 0 && len(m.allowedUsernames) == 0 {
		return false
	}
	if _, ok := m.allowedUserIDs[userID]; ok {
		return true
	}
	if username != "" {
		_, ok := m.allowedUsernames[strings.ToLower(username)]
		return ok
	}
	return false
}

func (m *telegramSessionMgr) getOrCreate(chatID int64) (*telegramSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[chatID]; ok {
		return sess, nil
	}

	engine, err := m.settings.NewEngine()
	if err != nil {
		return nil, fmt.Errorf("create engine: %w", err)
	}

	sess := &telegramSession{
		engine:       engine,
		lastActivity: time.Now(),
	}
	m.sessions[chatID] = sess
	return sess, nil
}

func (m *telegramSessionMgr) resetSession(chatID int64) (*telegramSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	engine, err := m.settings.NewEngine()
	if err != nil {
		return nil, fmt.Errorf("create engine: %w", err)
	}

	sess := &telegramSession{
		engine:       engine,
		lastActivity: time.Now(),
	}
	m.sessions[chatID] = sess
	return sess, nil
}

func (m *telegramSessionMgr) handleMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if !m.isAllowed(msg.From.ID, msg.From.UserName) {
		log.Printf("[telegram] ignoring message from unauthorised user %d (@%s)", msg.From.ID, msg.From.UserName)
		return
	}

	chatID := msg.Chat.ID

	if msg.IsCommand() {
		switch msg.Command() {
		case "start", "help":
			helpText := "I'm your AI assistant. Send me a message to get started!\n\n" +
				"Commands:\n" +
				"/reset  - Clear conversation history\n" +
				"/status - Show session info"
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, helpText))
			return

		case "reset":
			if _, err := m.resetSession(chatID); err != nil {
				_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Error resetting session: "+err.Error()))
				return
			}
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Conversation history cleared."))
			return

		case "status":
			sess, err := m.getOrCreate(chatID)
			if err != nil {
				_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Error: "+err.Error()))
				return
			}
			sess.mu.Lock()
			msgCount := len(sess.history)
			lastAct := sess.lastActivity
			sess.mu.Unlock()
			status := fmt.Sprintf("Session active\nMessages in history: %d\nLast activity: %s",
				msgCount, lastAct.Format(time.RFC3339))
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, status))
			return
		}
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	sess, err := m.getOrCreate(chatID)
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Error creating session: "+err.Error()))
		return
	}

	// Check idle timeout and reset if expired (under session lock).
	sess.mu.Lock()
	if time.Since(sess.lastActivity) > m.idleTimeout {
		sess.history = nil
		sess.engine.ResetConversation()
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "(Session reset due to inactivity)"))
	}
	sess.lastActivity = time.Now()
	sess.mu.Unlock()

	// Send "typing…" indicator.
	_, _ = bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))

	if err := m.streamReply(ctx, bot, sess, chatID, text); err != nil {
		log.Printf("[telegram] error streaming reply for chat %d: %v", chatID, err)
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Sorry, an error occurred: "+err.Error()))
	}
}

// streamReply streams an LLM response back to the chat via live message editing.
func (m *telegramSessionMgr) streamReply(ctx context.Context, bot *tgbotapi.BotAPI, sess *telegramSession, chatID int64, userText string) error {
	// We acquire the session lock for the entire streaming call so that
	// concurrent messages from the same chat are serialised.
	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Build full message list: system + history + new user turn.
	messages := make([]llm.Message, 0, len(sess.history)+2)
	if m.settings.SystemPrompt != "" && !containsSystemMsg(sess.history) {
		messages = append(messages, llm.SystemText(m.settings.SystemPrompt))
	}
	messages = append(messages, sess.history...)
	messages = append(messages, llm.UserText(userText))

	// Collect assistant and tool-result messages via the turn callback.
	var (
		producedMu sync.Mutex
		produced   []llm.Message
	)
	sess.engine.SetTurnCompletedCallback(func(_ context.Context, _ int, msgs []llm.Message, _ llm.TurnMetrics) error {
		producedMu.Lock()
		produced = append(produced, msgs...)
		producedMu.Unlock()
		return nil
	})
	defer sess.engine.SetTurnCompletedCallback(nil)

	req := llm.Request{
		Messages: messages,
		MaxTurns: m.settings.MaxTurns,
		Search:   m.settings.Search,
	}

	stream, err := sess.engine.Stream(ctx, req)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	defer stream.Close()

	// Send placeholder message to obtain a message ID for live editing.
	placeholder, err := bot.Send(tgbotapi.NewMessage(chatID, "⏳"))
	if err != nil {
		return fmt.Errorf("send placeholder: %w", err)
	}

	var (
		textMu     sync.Mutex
		textBuf    strings.Builder
		streamDone = make(chan error, 1)
	)

	// Goroutine: consume stream events.
	go func() {
		for {
			ev, recvErr := stream.Recv()
			if recvErr == io.EOF {
				streamDone <- nil
				return
			}
			if recvErr != nil {
				streamDone <- recvErr
				return
			}
			if ev.Type == llm.EventTextDelta {
				textMu.Lock()
				textBuf.WriteString(ev.Text)
				textMu.Unlock()
			}
		}
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	currentMsgID := placeholder.MessageID
	msgStart := 0 // byte offset in the full text where the current Telegram message begins

	sendEdit := func(msgID int, content string, withCursor bool) {
		if withCursor {
			content += "▌"
		}
		edit := tgbotapi.NewEditMessageText(chatID, msgID, content)
		_, _ = bot.Send(edit) // rate-limit errors are silently ignored
	}

	var streamErr error
loop:
	for {
		select {
		case err := <-streamDone:
			streamErr = err
			break loop
		case <-ticker.C:
			textMu.Lock()
			full := textBuf.String()
			textMu.Unlock()

			segment := full[msgStart:]
			if len(segment) == 0 {
				continue
			}

			if len(segment) >= telegramMaxMessageLen {
				// Finalize current message at the limit, then start a new one.
				sendEdit(currentMsgID, segment[:telegramMaxMessageLen], false)
				msgStart += telegramMaxMessageLen

				newMsg, sendErr := bot.Send(tgbotapi.NewMessage(chatID, "⏳"))
				if sendErr == nil {
					currentMsgID = newMsg.MessageID
				}
			} else {
				sendEdit(currentMsgID, segment, true)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if streamErr != nil {
		return streamErr
	}

	// Final edit: show full remaining text without cursor.
	textMu.Lock()
	full := textBuf.String()
	textMu.Unlock()

	segment := full[msgStart:]
	if segment == "" && full == "" {
		segment = "(no response)"
	}
	if segment != "" {
		sendEdit(currentMsgID, segment, false)
	}

	// Persist history: base + user message + produced (assistant + tool results).
	newHistory := make([]llm.Message, 0, len(sess.history)+2+len(produced))
	newHistory = append(newHistory, sess.history...)
	newHistory = append(newHistory, llm.UserText(userText))
	producedMu.Lock()
	newHistory = append(newHistory, produced...)
	producedMu.Unlock()
	// Fallback: if the callback didn't fire (no tools), record the text directly.
	if len(produced) == 0 && full != "" {
		newHistory = append(newHistory, llm.AssistantText(full))
	}
	sess.history = newHistory
	sess.lastActivity = time.Now()

	return nil
}

func containsSystemMsg(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			return true
		}
	}
	return false
}

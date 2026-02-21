package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm"
)

type serveJobStatus string

const (
	serveJobStatusQueued          serveJobStatus = "queued"
	serveJobStatusRunning         serveJobStatus = "running"
	serveJobStatusCancelRequested serveJobStatus = "cancel_requested"
	serveJobStatusCancelled       serveJobStatus = "cancelled"
	serveJobStatusCompleted       serveJobStatus = "completed"
	serveJobStatusFailed          serveJobStatus = "failed"
)

func (s serveJobStatus) isTerminal() bool {
	return s == serveJobStatusCancelled || s == serveJobStatusCompleted || s == serveJobStatusFailed
}

type serveJob struct {
	ID           string
	AgentName    string
	Instructions string
	InputRaw     json.RawMessage
	Status       serveJobStatus
	Thinking     string
	Response     string
	Error        string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	UpdatedAt    time.Time
	cancel       context.CancelFunc
}

type serveJobSnapshot struct {
	ID           string
	AgentName    string
	Instructions string
	Status       serveJobStatus
	Thinking     string
	Response     string
	Error        string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	UpdatedAt    time.Time
}

type serveJobsExecutor func(ctx context.Context, agentName, instructions string, onEvent func(llm.Event)) error

type serveJobsManager struct {
	workers  int
	executor serveJobsExecutor

	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	queue  []string
	jobs   map[string]*serveJob
	wg     sync.WaitGroup
}

func newServeJobsManager(workers int, executor serveJobsExecutor) *serveJobsManager {
	if workers <= 0 {
		workers = 1
	}
	if executor == nil {
		executor = func(context.Context, string, string, func(llm.Event)) error {
			return fmt.Errorf("jobs executor is not configured")
		}
	}
	m := &serveJobsManager{
		workers:  workers,
		executor: executor,
		queue:    make([]string, 0),
		jobs:     make(map[string]*serveJob),
	}
	m.cond = sync.NewCond(&m.mu)

	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m
}

func (m *serveJobsManager) worker() {
	defer m.wg.Done()
	for {
		id, ok := m.nextJobID()
		if !ok {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		if !m.startJob(id, cancel) {
			cancel()
			continue
		}
		err := m.executor(ctx, m.jobAgentName(id), m.jobInstructions(id), func(ev llm.Event) {
			m.applyEvent(id, ev)
		})
		cancel()
		m.finishJob(id, err)
	}
}

func (m *serveJobsManager) nextJobID() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for {
		if m.closed {
			return "", false
		}
		for len(m.queue) > 0 {
			id := m.queue[0]
			m.queue = m.queue[1:]
			job, ok := m.jobs[id]
			if !ok || job.Status != serveJobStatusQueued {
				continue
			}
			return id, true
		}
		m.cond.Wait()
	}
}

func (m *serveJobsManager) startJob(id string, cancel context.CancelFunc) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return false
	}
	if job.Status != serveJobStatusQueued {
		return false
	}

	now := time.Now()
	job.Status = serveJobStatusRunning
	job.StartedAt = &now
	job.UpdatedAt = now
	job.cancel = cancel
	return true
}

func (m *serveJobsManager) finishJob(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return
	}

	now := time.Now()
	job.UpdatedAt = now
	job.cancel = nil

	if job.Status == serveJobStatusCancelled {
		if job.CompletedAt == nil {
			job.CompletedAt = &now
		}
		return
	}

	if job.Status == serveJobStatusCancelRequested || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		job.Status = serveJobStatusCancelled
		job.CompletedAt = &now
		return
	}

	if err != nil {
		job.Status = serveJobStatusFailed
		job.Error = err.Error()
		job.CompletedAt = &now
		return
	}

	job.Status = serveJobStatusCompleted
	job.CompletedAt = &now
}

func (m *serveJobsManager) applyEvent(id string, ev llm.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return
	}
	if job.Status != serveJobStatusRunning && job.Status != serveJobStatusCancelRequested {
		return
	}

	switch ev.Type {
	case llm.EventReasoningDelta:
		if ev.Text != "" {
			job.Thinking += ev.Text
		}
	case llm.EventTextDelta:
		if ev.Text != "" {
			job.Response += ev.Text
		}
	}
	job.UpdatedAt = time.Now()
}

func (m *serveJobsManager) Enqueue(agentName, instructions string, inputRaw json.RawMessage) serveJobSnapshot {
	now := time.Now()
	job := &serveJob{
		ID:           "job_" + randomSuffix(),
		AgentName:    agentName,
		Instructions: instructions,
		InputRaw:     inputRaw,
		Status:       serveJobStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.queue = append(m.queue, job.ID)
	m.mu.Unlock()
	m.cond.Signal()

	return snapshotJob(job)
}

func (m *serveJobsManager) Get(id string) (serveJobSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return serveJobSnapshot{}, false
	}
	return snapshotJob(job), true
}

func (m *serveJobsManager) Cancel(id string) (serveJobSnapshot, bool, error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return serveJobSnapshot{}, false, fmt.Errorf("job not found")
	}

	now := time.Now()
	changed := false
	cancelFn := job.cancel
	switch job.Status {
	case serveJobStatusQueued:
		job.Status = serveJobStatusCancelled
		job.CompletedAt = &now
		job.UpdatedAt = now
		changed = true
	case serveJobStatusRunning:
		job.Status = serveJobStatusCancelRequested
		job.UpdatedAt = now
		changed = true
	case serveJobStatusCancelRequested:
		changed = true
	}
	snapshot := snapshotJob(job)
	m.mu.Unlock()

	if changed && cancelFn != nil {
		cancelFn()
	}
	return snapshot, changed, nil
}

func (m *serveJobsManager) List(offset, limit int, statusFilter string) ([]serveJobSnapshot, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var selected []serveJobSnapshot
	filterStatus := serveJobStatus(strings.ToLower(strings.TrimSpace(statusFilter)))
	for _, job := range m.jobs {
		if filterStatus != "" && job.Status != filterStatus {
			continue
		}
		selected = append(selected, snapshotJob(job))
	}

	sort.Slice(selected, func(i, j int) bool {
		if selected[i].CreatedAt.Equal(selected[j].CreatedAt) {
			return selected[i].ID > selected[j].ID
		}
		return selected[i].CreatedAt.After(selected[j].CreatedAt)
	})

	total := len(selected)
	if offset >= total {
		return []serveJobSnapshot{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return selected[offset:end], total
}

func (m *serveJobsManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	var cancels []context.CancelFunc
	for _, job := range m.jobs {
		if job.cancel != nil {
			cancels = append(cancels, job.cancel)
		}
	}
	m.mu.Unlock()
	m.cond.Broadcast()

	for _, cancel := range cancels {
		cancel()
	}
	m.wg.Wait()
}

func (m *serveJobsManager) jobAgentName(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ""
	}
	return job.AgentName
}

func (m *serveJobsManager) jobInstructions(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ""
	}
	return job.Instructions
}

func snapshotJob(job *serveJob) serveJobSnapshot {
	var startedAt *time.Time
	if job.StartedAt != nil {
		started := *job.StartedAt
		startedAt = &started
	}
	var completedAt *time.Time
	if job.CompletedAt != nil {
		completed := *job.CompletedAt
		completedAt = &completed
	}
	return serveJobSnapshot{
		ID:           job.ID,
		AgentName:    job.AgentName,
		Instructions: job.Instructions,
		Status:       job.Status,
		Thinking:     job.Thinking,
		Response:     job.Response,
		Error:        job.Error,
		CreatedAt:    job.CreatedAt,
		StartedAt:    startedAt,
		CompletedAt:  completedAt,
		UpdatedAt:    job.UpdatedAt,
	}
}

func (s *serveServer) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobsMgr == nil {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handleCreateJob(w, r)
	case http.MethodGet:
		s.handleListJobs(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

func (s *serveServer) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if s.jobsMgr == nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/jobs/") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"))
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		job, ok := s.jobsMgr.Get(id)
		if !ok {
			writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "job not found")
			return
		}
		writeJSON(w, http.StatusOK, jobDetailPayload(job))
	case http.MethodDelete:
		job, changed, err := s.jobsMgr.Cancel(id)
		if err != nil {
			writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "job not found")
			return
		}
		if changed {
			writeJSON(w, http.StatusAccepted, jobDetailPayload(job))
			return
		}
		writeJSON(w, http.StatusOK, jobDetailPayload(job))
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

func (s *serveServer) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if err := requireJSONContentType(r); err != nil {
		writeOpenAIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", err.Error())
		return
	}

	var raw map[string]json.RawMessage
	if err := decodeJSONBody(r, &raw); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	agentName := strings.TrimSpace(jsonString(raw["agent_name"]))
	instructions := strings.TrimSpace(jsonString(raw["instructions"]))
	if agentName == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "agent_name is required")
		return
	}
	if instructions == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "instructions is required")
		return
	}

	if s.cfgRef != nil {
		if _, err := LoadAgent(agentName, s.cfgRef); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
	}

	rawPayload, _ := json.Marshal(raw)
	job := s.jobsMgr.Enqueue(agentName, instructions, rawPayload)
	writeJSON(w, http.StatusAccepted, jobDetailPayload(job))
}

func (s *serveServer) handleListJobs(w http.ResponseWriter, r *http.Request) {
	offset, err := parseNonNegativeIntQuery(r, "offset", 0)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	limit, err := parseNonNegativeIntQuery(r, "limit", 20)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if statusFilter != "" && !isValidServeJobStatus(serveJobStatus(statusFilter)) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid status filter")
		return
	}

	items, total := s.jobsMgr.List(offset, limit, statusFilter)
	payloadItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		payloadItems = append(payloadItems, jobSummaryPayload(item))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   payloadItems,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	})
}

func parseNonNegativeIntQuery(r *http.Request, key string, defaultValue int) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func isValidServeJobStatus(status serveJobStatus) bool {
	switch status {
	case serveJobStatusQueued, serveJobStatusRunning, serveJobStatusCancelRequested, serveJobStatusCancelled, serveJobStatusCompleted, serveJobStatusFailed:
		return true
	default:
		return false
	}
}

func jobSummaryPayload(job serveJobSnapshot) map[string]any {
	return map[string]any{
		"id":           job.ID,
		"object":       "job",
		"status":       string(job.Status),
		"agent_name":   job.AgentName,
		"instructions": job.Instructions,
		"created_at":   job.CreatedAt.Format(time.RFC3339Nano),
		"started_at":   formatOptionalTime(job.StartedAt),
		"completed_at": formatOptionalTime(job.CompletedAt),
		"updated_at":   job.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func jobDetailPayload(job serveJobSnapshot) map[string]any {
	payload := jobSummaryPayload(job)
	payload["thinking"] = job.Thinking
	payload["response"] = job.Response
	if job.Error != "" {
		payload["error"] = job.Error
	}
	return payload
}

func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func cloneConfigForServeJob(src *config.Config) *config.Config {
	if src == nil {
		return &config.Config{}
	}
	clone := *src
	if src.Providers != nil {
		clone.Providers = make(map[string]config.ProviderConfig, len(src.Providers))
		for name, cfg := range src.Providers {
			clone.Providers[name] = cfg
		}
	}
	if src.Agents.Preferences != nil {
		clone.Agents.Preferences = make(map[string]config.AgentPreference, len(src.Agents.Preferences))
		for name, pref := range src.Agents.Preferences {
			clone.Agents.Preferences[name] = pref
		}
	}
	return &clone
}

func newServeJobsExecutor(baseCfg *config.Config) serveJobsExecutor {
	return func(ctx context.Context, agentName, instructions string, onEvent func(llm.Event)) error {
		jobCfg := cloneConfigForServeJob(baseCfg)

		agent, err := LoadAgent(agentName, jobCfg)
		if err != nil {
			return err
		}
		if agent == nil {
			return fmt.Errorf("agent %q not found", agentName)
		}

		if err := applyProviderOverridesWithAgent(jobCfg, jobCfg.Ask.Provider, jobCfg.Ask.Model, serveProvider, agent.Provider, agent.Model); err != nil {
			return err
		}

		settings, err := ResolveSettings(jobCfg, agent, CLIFlags{
			Provider:      serveProvider,
			Tools:         serveTools,
			ReadDirs:      serveReadDirs,
			WriteDirs:     serveWriteDirs,
			ShellAllow:    serveShellAllow,
			MCP:           serveMCP,
			SystemMessage: serveSystemMessage,
			MaxTurns:      serveMaxTurns,
			Search:        serveSearch,
		}, jobCfg.Ask.Provider, jobCfg.Ask.Model, jobCfg.Ask.Instructions, jobCfg.Ask.MaxTurns, 20)
		if err != nil {
			return err
		}

		modelName := activeModel(jobCfg)
		provider, err := llm.NewProvider(jobCfg)
		if err != nil {
			return err
		}

		engine, toolMgr, err := newServeEngineWithTools(jobCfg, settings, provider, serveYolo, WireSpawnAgentRunner)
		if err != nil {
			return err
		}

		forceExternalSearch := resolveForceExternalSearch(jobCfg, serveNativeSearch, serveNoNativeSearch)
		runtime := &serveRuntime{
			provider:            provider,
			engine:              engine,
			toolMgr:             toolMgr,
			systemPrompt:        settings.SystemPrompt,
			search:              settings.Search,
			forceExternalSearch: forceExternalSearch,
			maxTurns:            settings.MaxTurns,
			debug:               serveDebug,
			debugRaw:            debugRaw,
			defaultModel:        modelName,
			toolsSetting:        settings.Tools,
			mcpSetting:          settings.MCP,
			agentName:           agent.Name,
		}
		defer runtime.Close()

		if settings.MCP != "" {
			mcpOpts := &MCPOptions{Provider: provider, Model: modelName, YoloMode: serveYolo}
			mgr, err := enableMCPServersWithFeedback(ctx, settings.MCP, engine, io.Discard, mcpOpts)
			if err != nil {
				return err
			}
			runtime.mcpManager = mgr
		}

		llmReq := llm.Request{
			Model:               modelName,
			Tools:               runtime.selectTools(nil),
			ToolChoice:          llm.ToolChoice{Mode: llm.ToolChoiceAuto},
			ParallelToolCalls:   true,
			Search:              settings.Search,
			ForceExternalSearch: forceExternalSearch,
			MaxTurns:            settings.MaxTurns,
			Debug:               serveDebug,
			DebugRaw:            debugRaw,
		}

		_, err = runtime.RunWithEvents(ctx, false, false, []llm.Message{llm.UserText(instructions)}, llmReq, func(ev llm.Event) error {
			if onEvent != nil {
				onEvent(ev)
			}
			return nil
		})
		return err
	}
}

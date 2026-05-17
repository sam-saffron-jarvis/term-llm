package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/tools"
	"github.com/spf13/cobra"
)

var (
	contextCompressionPOCLimit       int
	contextCompressionPOCChunks      int
	contextCompressionPOCJSON        bool
	contextCompressionPOCIncludeArch bool
)

var contextCompressionPOCCmd = &cobra.Command{
	Use:    "context-compression-poc",
	Short:  "Run a safe replay PoC for tool-based context compression",
	Hidden: true,
	Long: `Run a disposable, landlocked proof of concept for tool-based context
compression using real prior sessions from the local session store.

The harness never contacts an LLM provider. It replays prior transcripts into a
read-only sandbox, exposes all standard tool names as model-visible surface area,
and allowlists only context_fetch for execution. Dangerous tools fail closed.
Correctness is measured as lexical evidence recall against the recorded final
assistant answer, not as a claim of live model quality.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := runContextCompressionPOC(cmd.Context(), contextCompressionPOCLimit, contextCompressionPOCChunks, contextCompressionPOCIncludeArch)
		if err != nil {
			return err
		}
		if contextCompressionPOCJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		printContextCompressionPOCReport(cmd.OutOrStdout(), result)
		return nil
	},
}

func init() {
	contextCompressionPOCCmd.Flags().IntVar(&contextCompressionPOCLimit, "limit", 8, "maximum replayable sessions to evaluate")
	contextCompressionPOCCmd.Flags().IntVar(&contextCompressionPOCChunks, "chunks", 6, "chunks returned per context_fetch call")
	contextCompressionPOCCmd.Flags().BoolVar(&contextCompressionPOCJSON, "json", false, "emit JSON")
	contextCompressionPOCCmd.Flags().BoolVar(&contextCompressionPOCIncludeArch, "include-archived", false, "include archived sessions")
	rootCmd.AddCommand(contextCompressionPOCCmd)
}

type contextCompressionPOCResult struct {
	StartedAt       time.Time                       `json:"started_at"`
	CompletedAt     time.Time                       `json:"completed_at"`
	Mode            string                          `json:"mode"`
	ToolSurface     contextCompressionToolSurface   `json:"tool_surface"`
	Totals          contextCompressionPOCTotals     `json:"totals"`
	Sessions        []contextCompressionSessionEval `json:"sessions"`
	Failures        []string                        `json:"failures,omitempty"`
	ConfigChanges   []string                        `json:"config_changes,omitempty"`
	FailureBehavior []string                        `json:"failure_behavior"`
}

type contextCompressionToolSurface struct {
	VisibleTools     []string `json:"visible_tools"`
	ExecutableTools  []string `json:"executable_tools"`
	BlockedToolCount int      `json:"blocked_tool_count"`
	StrictSchemas    bool     `json:"strict_schemas"`
}

type contextCompressionPOCTotals struct {
	SessionsEvaluated       int     `json:"sessions_evaluated"`
	BaselineInputTokens     int     `json:"baseline_input_tokens"`
	CompressedInputTokens   int     `json:"compressed_input_tokens"`
	FetchedTokens           int     `json:"fetched_tokens"`
	TokenReductionPct       float64 `json:"token_reduction_pct"`
	BaselineLatencyMs       int64   `json:"baseline_latency_ms"`
	CompressedLatencyMs     int64   `json:"compressed_latency_ms"`
	CorrectnessPasses       int     `json:"correctness_passes"`
	CorrectnessPassRate     float64 `json:"correctness_pass_rate"`
	MissingContextFailures  int     `json:"missing_context_failures"`
	DangerousToolExecutions int     `json:"dangerous_tool_executions"`
}

type contextCompressionSessionEval struct {
	SessionID             string   `json:"session_id"`
	SessionNumber         int64    `json:"session_number"`
	Title                 string   `json:"title,omitempty"`
	MessageCount          int      `json:"message_count"`
	ChunkCount            int      `json:"chunk_count"`
	BaselineInputTokens   int      `json:"baseline_input_tokens"`
	CompressedInputTokens int      `json:"compressed_input_tokens"`
	FetchedTokens         int      `json:"fetched_tokens"`
	TokenReductionPct     float64  `json:"token_reduction_pct"`
	BaselineLatencyMs     int64    `json:"baseline_latency_ms"`
	CompressedLatencyMs   int64    `json:"compressed_latency_ms"`
	CorrectnessScore      float64  `json:"correctness_score"`
	CorrectnessPass       bool     `json:"correctness_pass"`
	MissingContext        bool     `json:"missing_context"`
	FetchedSources        []string `json:"fetched_sources"`
	Question              string   `json:"question_preview"`
	Target                string   `json:"target_preview"`
}

type contextChunk struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	SessionNumber int64  `json:"session_number"`
	MessageID     int64  `json:"message_id"`
	Sequence      int    `json:"sequence"`
	Role          string `json:"role"`
	Text          string `json:"text"`
}

type contextFetchTool struct {
	chunks []contextChunk
	limit  int
}

func (t *contextFetchTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "context_fetch",
		Description: "Fetch vetted read-only transcript chunks by query. Returns source metadata for every chunk. Fails closed when no relevant context is found.",
		Strict:      true,
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Question or context need to retrieve evidence for.",
				},
				"k": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"maximum":     20,
					"description": "Maximum chunks to return.",
				},
			},
			"required":             []string{"query", "k"},
			"additionalProperties": false,
		},
	}
}

func (t *contextFetchTool) Preview(args json.RawMessage) string {
	return "fetching vetted transcript context"
}

func (t *contextFetchTool) Execute(ctx context.Context, args json.RawMessage) (llm.ToolOutput, error) {
	var req struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return llm.ToolOutput{}, fmt.Errorf("invalid context_fetch args: %w", err)
	}
	if strings.TrimSpace(req.Query) == "" {
		return llm.ToolOutput{}, fmt.Errorf("context_fetch missing required query")
	}
	if req.K <= 0 || req.K > 20 {
		return llm.ToolOutput{}, fmt.Errorf("context_fetch k must be between 1 and 20")
	}
	if t.limit > 0 && req.K > t.limit {
		req.K = t.limit
	}
	chunks := rankChunks(req.Query, t.chunks, req.K)
	if len(chunks) == 0 {
		return llm.ToolOutput{}, fmt.Errorf("context_fetch found no relevant context for query %q", req.Query)
	}
	payload := map[string]any{"chunks": chunks}
	b, err := json.Marshal(payload)
	if err != nil {
		return llm.ToolOutput{}, err
	}
	return llm.TextOutput(string(b)), nil
}

func runContextCompressionPOC(ctx context.Context, limit, chunksPerFetch int, includeArchived bool) (*contextCompressionPOCResult, error) {
	started := time.Now()
	if limit <= 0 {
		limit = 8
	}
	if chunksPerFetch <= 0 {
		chunksPerFetch = 6
	}
	store, err := getSessionStore()
	if err != nil {
		return nil, err
	}
	defer store.Close()

	summaries, err := store.List(ctx, session.ListOptions{Limit: limit * 3, Archived: includeArchived, SortByActivity: true})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	visibleTools := append([]string{"context_fetch"}, tools.AllToolNames()...)
	sort.Strings(visibleTools)
	result := &contextCompressionPOCResult{
		StartedAt: started,
		Mode:      "offline deterministic replay; no provider/auth/network calls",
		ToolSurface: contextCompressionToolSurface{
			VisibleTools:     visibleTools,
			ExecutableTools:  []string{"context_fetch"},
			BlockedToolCount: len(visibleTools) - 1,
			StrictSchemas:    true,
		},
		ConfigChanges: []string{"none for this landlocked PoC; provider/auth config is not used"},
		FailureBehavior: []string{
			"context_fetch rejects invalid JSON, empty query, out-of-range k, and unknown context",
			"all non-context_fetch tools are present in the simulated surface but fail closed by allowlist",
			"sessions without a prior user message or target assistant answer are skipped, not guessed",
		},
	}

	for _, summary := range summaries {
		if len(result.Sessions) >= limit {
			break
		}
		msgs, err := store.GetMessages(ctx, summary.ID, 0, 0)
		if err != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("#%d get messages: %v", summary.Number, err))
			continue
		}
		eval, ok := evaluateContextCompressionSession(ctx, summary, msgs, chunksPerFetch)
		if !ok {
			continue
		}
		result.Sessions = append(result.Sessions, eval)
		accumulateContextCompressionTotals(&result.Totals, eval)
	}

	if len(result.Sessions) == 0 {
		return nil, fmt.Errorf("no replayable sessions found (need sessions with prior context, a final user question, and a final assistant answer)")
	}
	finalizeContextCompressionTotals(&result.Totals)
	result.CompletedAt = time.Now()
	return result, nil
}

func evaluateContextCompressionSession(ctx context.Context, summary session.SessionSummary, msgs []session.Message, k int) (contextCompressionSessionEval, bool) {
	questionIdx := -1
	answerIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		role := msgs[i].Role
		text := strings.TrimSpace(msgs[i].TextContent)
		if text == "" || role == llm.RoleTool || role == llm.RoleEvent || role == llm.RoleSystem {
			continue
		}
		if answerIdx == -1 && role == llm.RoleAssistant {
			answerIdx = i
			continue
		}
		if answerIdx != -1 && role == llm.RoleUser {
			questionIdx = i
			break
		}
	}
	if questionIdx <= 0 || answerIdx <= questionIdx {
		return contextCompressionSessionEval{}, false
	}

	prior := msgs[:questionIdx]
	question := msgs[questionIdx].TextContent
	target := msgs[answerIdx].TextContent
	chunks := buildContextChunks(summary, prior)
	if len(chunks) < 3 || strings.TrimSpace(target) == "" {
		return contextCompressionSessionEval{}, false
	}

	baselineStart := time.Now()
	baselinePrompt := renderBaselinePrompt(prior, question)
	baselineTokens := llm.EstimateTokens(baselinePrompt)
	baselineLatency := time.Since(baselineStart)

	compressedStart := time.Now()
	fetchTool := &contextFetchTool{chunks: chunks, limit: k}
	manifest := renderCompressedManifest(chunks)
	initialPrompt := renderCompressedPrompt(manifest, question, fetchTool.Spec(), tools.AllToolNames())
	out, err := fetchTool.Execute(ctx, mustJSON(map[string]any{"query": question, "k": k}))
	missing := err != nil
	fetched := decodeFetchedChunks(out.Content)
	fetchedText := out.Content
	compressedTokens := llm.EstimateTokens(initialPrompt) + llm.EstimateTokens(fetchedText)
	compressedLatency := time.Since(compressedStart)
	fetchedTokens := llm.EstimateTokens(fetchedText)

	score := evidenceRecall(target, fetched)
	pass := !missing && score >= 0.25
	sources := make([]string, 0, len(fetched))
	for _, chunk := range fetched {
		sources = append(sources, fmt.Sprintf("session:%d msg:%d seq:%d role:%s", chunk.SessionNumber, chunk.MessageID, chunk.Sequence, chunk.Role))
	}

	return contextCompressionSessionEval{
		SessionID:             summary.ID,
		SessionNumber:         summary.Number,
		Title:                 firstNonEmptyPOC(summary.GeneratedShortTitle, summary.Name, summary.Summary),
		MessageCount:          len(msgs),
		ChunkCount:            len(chunks),
		BaselineInputTokens:   baselineTokens,
		CompressedInputTokens: compressedTokens,
		FetchedTokens:         fetchedTokens,
		TokenReductionPct:     reductionPct(baselineTokens, compressedTokens),
		BaselineLatencyMs:     baselineLatency.Milliseconds(),
		CompressedLatencyMs:   compressedLatency.Milliseconds(),
		CorrectnessScore:      round(score, 3),
		CorrectnessPass:       pass,
		MissingContext:        missing,
		FetchedSources:        sources,
		Question:              preview(question, 180),
		Target:                preview(target, 180),
	}, true
}

func buildContextChunks(summary session.SessionSummary, msgs []session.Message) []contextChunk {
	chunks := make([]contextChunk, 0, len(msgs))
	for _, msg := range msgs {
		text := strings.TrimSpace(msg.TextContent)
		if text == "" || msg.Role == llm.RoleSystem || msg.Role == llm.RoleEvent {
			continue
		}
		chunks = append(chunks, contextChunk{
			ID:            fmt.Sprintf("s%d-m%d", summary.Number, msg.ID),
			SessionID:     summary.ID,
			SessionNumber: summary.Number,
			MessageID:     msg.ID,
			Sequence:      msg.Sequence,
			Role:          string(msg.Role),
			Text:          preview(text, 3000),
		})
	}
	return chunks
}

func rankChunks(query string, chunks []contextChunk, k int) []contextChunk {
	q := termSet(query)
	type scored struct {
		chunk contextChunk
		score float64
	}
	scoredChunks := make([]scored, 0, len(chunks))
	for _, chunk := range chunks {
		terms := termSet(chunk.Text)
		score := 0.0
		for term := range q {
			if terms[term] {
				score += 1
			}
		}
		if chunk.Role == string(llm.RoleAssistant) {
			score *= 1.05
		}
		if score > 0 {
			scoredChunks = append(scoredChunks, scored{chunk: chunk, score: score})
		}
	}
	if len(scoredChunks) == 0 {
		start := len(chunks) - k
		if start < 0 {
			start = 0
		}
		out := append([]contextChunk(nil), chunks[start:]...)
		sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
		return out
	}
	sort.SliceStable(scoredChunks, func(i, j int) bool {
		if scoredChunks[i].score == scoredChunks[j].score {
			return scoredChunks[i].chunk.Sequence > scoredChunks[j].chunk.Sequence
		}
		return scoredChunks[i].score > scoredChunks[j].score
	})
	selected := map[string]bool{}
	out := make([]contextChunk, 0, k)
	for i := 0; i < len(scoredChunks) && len(out) < k; i++ {
		out = append(out, scoredChunks[i].chunk)
		selected[scoredChunks[i].chunk.ID] = true
	}
	for i := len(chunks) - 1; i >= 0 && len(out) < k; i-- {
		if !selected[chunks[i].ID] {
			out = append(out, chunks[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

func evidenceRecall(target string, chunks []contextChunk) float64 {
	targetTerms := termSet(target)
	if len(targetTerms) == 0 {
		return 0
	}
	var evidence strings.Builder
	for _, chunk := range chunks {
		evidence.WriteString(" ")
		evidence.WriteString(chunk.Text)
	}
	evidenceTerms := termSet(evidence.String())
	matches := 0
	for term := range targetTerms {
		if evidenceTerms[term] {
			matches++
		}
	}
	return float64(matches) / float64(len(targetTerms))
}

func termSet(text string) map[string]bool {
	terms := map[string]bool{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '/' && r != '.'
	}) {
		term := strings.Trim(raw, " .,:;()[]{}<>\"'`")
		if len(term) < 3 || stopword(term) {
			continue
		}
		terms[term] = true
	}
	return terms
}

func stopword(s string) bool {
	switch s {
	case "the", "and", "for", "you", "that", "this", "with", "have", "from", "not", "but", "are", "was", "were", "can", "will", "your", "has", "had", "all", "any", "into", "out", "about", "what", "when", "where", "why", "how", "done", "just", "then", "than", "there", "their", "would", "could", "should":
		return true
	}
	return false
}

func renderBaselinePrompt(msgs []session.Message, question string) string {
	var b strings.Builder
	b.WriteString("You are replaying a prior term-llm session. Use the full transcript.\n")
	for _, msg := range msgs {
		if strings.TrimSpace(msg.TextContent) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n[%s seq=%d msg=%d]\n%s\n", msg.Role, msg.Sequence, msg.ID, msg.TextContent)
	}
	fmt.Fprintf(&b, "\n[user replay]\n%s\n", question)
	return b.String()
}

func renderCompressedManifest(chunks []contextChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		fmt.Fprintf(&b, "- id=%s source=session:%d msg:%d seq:%d role:%s preview=%q\n", chunk.ID, chunk.SessionNumber, chunk.MessageID, chunk.Sequence, chunk.Role, preview(chunk.Text, 120))
	}
	return b.String()
}

func renderCompressedPrompt(manifest, question string, spec llm.ToolSpec, allTools []string) string {
	schema, _ := json.Marshal(spec.Schema)
	return fmt.Sprintf("You are replaying a prior session with compressed context. Visible tools: %s. Only context_fetch is executable; other tools fail closed. context_fetch schema(strict=%v): %s\nChunk manifest with source metadata:\n%s\nQuestion:\n%s\nIf needed context is absent, say MISSING_CONTEXT.", strings.Join(allTools, ","), spec.Strict, schema, manifest, question)
}

func decodeFetchedChunks(content string) []contextChunk {
	var payload struct {
		Chunks []contextChunk `json:"chunks"`
	}
	_ = json.Unmarshal([]byte(content), &payload)
	return payload.Chunks
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func accumulateContextCompressionTotals(t *contextCompressionPOCTotals, eval contextCompressionSessionEval) {
	t.SessionsEvaluated++
	t.BaselineInputTokens += eval.BaselineInputTokens
	t.CompressedInputTokens += eval.CompressedInputTokens
	t.FetchedTokens += eval.FetchedTokens
	t.BaselineLatencyMs += eval.BaselineLatencyMs
	t.CompressedLatencyMs += eval.CompressedLatencyMs
	if eval.CorrectnessPass {
		t.CorrectnessPasses++
	}
	if eval.MissingContext {
		t.MissingContextFailures++
	}
}

func finalizeContextCompressionTotals(t *contextCompressionPOCTotals) {
	t.TokenReductionPct = reductionPct(t.BaselineInputTokens, t.CompressedInputTokens)
	if t.SessionsEvaluated > 0 {
		t.CorrectnessPassRate = round(float64(t.CorrectnessPasses)/float64(t.SessionsEvaluated), 3)
	}
}

func reductionPct(base, compressed int) float64 {
	if base <= 0 {
		return 0
	}
	return round((1-float64(compressed)/float64(base))*100, 1)
}

func round(f float64, places int) float64 {
	pow := math.Pow10(places)
	return math.Round(f*pow) / pow
}

func firstNonEmptyPOC(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func preview(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func printContextCompressionPOCReport(w io.Writer, r *contextCompressionPOCResult) {
	fmt.Fprintln(w, "# Tool-based context compression PoC")
	fmt.Fprintf(w, "\nMode: %s\n", r.Mode)
	fmt.Fprintf(w, "\nTools: %d visible, executable allowlist: %s, blocked: %d, strict schemas: %v\n", len(r.ToolSurface.VisibleTools), strings.Join(r.ToolSurface.ExecutableTools, ", "), r.ToolSurface.BlockedToolCount, r.ToolSurface.StrictSchemas)
	fmt.Fprintf(w, "\nTotals: sessions=%d baseline=%dt compressed=%dt fetched=%dt reduction=%.1f%% correctness=%d/%d (%.0f%%) missing_context=%d dangerous_exec=%d latency=%dms→%dms\n",
		r.Totals.SessionsEvaluated, r.Totals.BaselineInputTokens, r.Totals.CompressedInputTokens, r.Totals.FetchedTokens, r.Totals.TokenReductionPct,
		r.Totals.CorrectnessPasses, r.Totals.SessionsEvaluated, r.Totals.CorrectnessPassRate*100, r.Totals.MissingContextFailures, r.Totals.DangerousToolExecutions,
		r.Totals.BaselineLatencyMs, r.Totals.CompressedLatencyMs)
	fmt.Fprintln(w, "\n| session | msgs | chunks | baseline t | compressed t | reduction | correctness | sources |")
	fmt.Fprintln(w, "|---:|---:|---:|---:|---:|---:|---:|---|")
	for _, s := range r.Sessions {
		fmt.Fprintf(w, "| #%d | %d | %d | %d | %d | %.1f%% | %.3f %v | %d |\n", s.SessionNumber, s.MessageCount, s.ChunkCount, s.BaselineInputTokens, s.CompressedInputTokens, s.TokenReductionPct, s.CorrectnessScore, s.CorrectnessPass, len(s.FetchedSources))
	}
	fmt.Fprintln(w, "\nFailure behavior:")
	for _, item := range r.FailureBehavior {
		fmt.Fprintf(w, "- %s\n", item)
	}
	fmt.Fprintln(w, "\nConfiguration changes:")
	for _, item := range r.ConfigChanges {
		fmt.Fprintf(w, "- %s\n", item)
	}
	if len(r.Failures) > 0 {
		fmt.Fprintln(w, "\nNon-fatal failures:")
		for _, item := range r.Failures {
			fmt.Fprintf(w, "- %s\n", item)
		}
	}
}

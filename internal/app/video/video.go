// Package video implements recoverable video generation and existing-job retrieval.
package video

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/atomicfile"
	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/providerhttp"
	media "github.com/term-llm/term-llm/internal/video"
)

// Retriever deliberately cannot quote or queue a paid job.
type Retriever interface {
	RetrieveTo(context.Context, media.QueuedJob, io.Writer, bool) (*media.Retrieval, error)
}
type Provider interface {
	Retriever
	Quote(context.Context, media.Request) (*media.Quote, error)
	Queue(context.Context, media.Request) (*media.QueuedJob, error)
}
type Completer interface {
	Complete(context.Context, media.QueuedJob) error
}

type Options struct {
	Request                                    media.Request
	QueueID                                    string
	Output, OutputDir                          string
	QuoteOnly, SkipQuote, NoWait, DeleteRemote bool
	PollInterval, Timeout                      time.Duration
	Stderr                                     io.Writer
	LoadInputs                                 func(*media.Request) error
	ReferencePaths                             []string
}
type Quote struct {
	Amount float64 `json:"amount"`
}
type Job struct {
	QueueID string `json:"queue_id"`
}
type Output struct {
	Path     string `json:"path"`
	MimeType string `json:"mime_type,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
}
type Result struct {
	Provider    string   `json:"provider"`
	Prompt      string   `json:"prompt,omitempty"`
	Model       string   `json:"model"`
	Duration    string   `json:"duration,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	Status      string   `json:"status"`
	Quote       *Quote   `json:"quote,omitempty"`
	Job         *Job     `json:"job,omitempty"`
	Output      *Output  `json:"output,omitempty"`
	Input       string   `json:"input,omitempty"`
	References  []string `json:"references,omitempty"`
	Recovery    string   `json:"recovery,omitempty"`
	Error       string   `json:"error,omitempty"`
	Warning     string   `json:"warning,omitempty"`
}

func initial(o Options) Result {
	r := Result{Provider: "venice", Prompt: o.Request.Prompt, Model: o.Request.Model, Duration: o.Request.Duration, AspectRatio: o.Request.AspectRatio, Status: "pending", Input: mediautil.DiagnosticReference(strings.TrimSpace(o.Request.ImagePath)), References: diagnosticReferences(o.ReferencePaths)}
	if o.Request.Resolution != nil {
		r.Resolution = *o.Request.Resolution
	}
	return r
}

// Generate quotes before loading inputs, queues once, and emits the receipt even
// when stdout is reserved for JSON. A failed queue is never automatically retried.
func Generate(ctx context.Context, p Provider, o Options) (Result, error) {
	r := initial(o)
	if err := media.ValidateRequest(o.Request); err != nil {
		return fail(r, "invalid", err)
	}
	if validator, ok := p.(interface{ ValidateRequest(media.Request) error }); ok {
		if err := validator.ValidateRequest(o.Request); err != nil {
			return fail(r, "invalid", err)
		}
	}
	if o.Timeout < 0 {
		return fail(r, "invalid", fmt.Errorf("timeout must not be negative"))
	}
	if o.DeleteRemote && (o.NoWait || o.QuoteOnly) {
		return fail(r, "invalid", fmt.Errorf("--delete-remote requires waiting for a durable save; conflicts with --no-wait/--quote-only"))
	}
	if o.SkipQuote && o.QuoteOnly {
		return fail(r, "invalid", fmt.Errorf("--skip-quote conflicts with --quote-only"))
	}
	if o.DeleteRemote {
		if _, ok := p.(Completer); !ok {
			return fail(r, "invalid", fmt.Errorf("provider does not support separate safe cleanup"))
		}
	}
	var file *os.File
	var path string
	var err error
	if !o.QuoteOnly {
		file, path, err = prepare(o)
		if err != nil {
			return fail(r, "save_failed", err)
		}
		defer discard(file)
	}
	ctx, cancel := requestContext(ctx, o.Timeout)
	defer cancel()
	if !o.SkipQuote {
		q, err := p.Quote(ctx, o.Request)
		if err != nil {
			return fail(r, "quote_failed", fmt.Errorf("video quote: %w", err))
		}
		r.Quote = &Quote{q.Amount}
		if o.Stderr != nil {
			fmt.Fprintf(o.Stderr, "Estimated cost: $%.2f\n", q.Amount)
		}
	}
	if o.QuoteOnly {
		r.Status = "quoted"
		return r, nil
	}
	if o.LoadInputs != nil {
		if err := o.LoadInputs(&o.Request); err != nil {
			return fail(r, "input_failed", err)
		}
	}
	job, err := p.Queue(ctx, o.Request)
	if err != nil {
		var notSent *media.RequestNotSentError
		if errors.As(err, &notSent) {
			return fail(r, "invalid", err)
		}
		if queueRejected(err) {
			return fail(r, "queue_rejected", fmt.Errorf("video queue rejected: %w", err))
		}
		return fail(r, "queue_outcome_unknown", fmt.Errorf("video queue outcome unknown: a paid job may already exist; do not requeue: %w", err))
	}
	r = withJob(r, *job)
	receipt(o, *job)
	if o.NoWait {
		r.Status = "queued"
		return r, nil
	}
	return poll(ctx, p, o, r, *job, file, path)
}

// RetrieveExisting has no route to Quote or Queue and never loads original files.
func RetrieveExisting(ctx context.Context, p Retriever, o Options) (Result, error) {
	r := initial(o)
	r.Duration = ""
	job := media.QueuedJob{Model: o.Request.Model, QueueID: o.QueueID}
	r = withJob(r, job)
	if job.Model == "" || job.QueueID == "" {
		return fail(r, "invalid", fmt.Errorf("--queue-id requires --model"))
	}
	if o.DeleteRemote {
		if _, ok := p.(Completer); !ok {
			return fail(r, "invalid", fmt.Errorf("provider does not support separate safe cleanup"))
		}
	}
	file, path, err := prepare(o)
	if err != nil {
		return fail(r, "save_failed", err)
	}
	defer discard(file)
	ctx, cancel := requestContext(ctx, o.Timeout)
	defer cancel()
	return poll(ctx, p, o, r, job, file, path)
}

func receipt(o Options, job media.QueuedJob) {
	if o.Stderr != nil {
		fmt.Fprintf(o.Stderr, "Queued video: model=%s queue_id=%s\n", job.Model, job.QueueID)
	}
}
func withJob(r Result, job media.QueuedJob) Result {
	r.Model = job.Model
	r.Job = &Job{job.QueueID}
	r.Recovery = "term-llm video --model " + shellQuote(job.Model) + " --queue-id " + shellQuote(job.QueueID)
	return r
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func fail(r Result, status string, err error) (Result, error) {
	r.Status = status
	r.Error = err.Error()
	if r.Recovery != "" {
		err = fmt.Errorf("%w; recover without requeueing: %s", err, r.Recovery)
	}
	return r, err
}

func poll(ctx context.Context, p Retriever, o Options, r Result, job media.QueuedJob, file *os.File, path string) (Result, error) {
	for {
		if err := ctx.Err(); err != nil {
			return interrupted(r, err)
		}
		if err := file.Truncate(0); err != nil {
			return fail(r, "save_failed", err)
		}
		if _, err := file.Seek(0, 0); err != nil {
			return fail(r, "save_failed", err)
		}
		dst := &saveWriter{Writer: file}
		got, err := p.RetrieveTo(ctx, job, dst, o.Request.Debug || o.Request.DebugRaw)
		if dst.err != nil {
			return fail(r, "save_failed", fmt.Errorf("write video: %w", dst.err))
		}
		if err != nil {
			if ctx.Err() != nil {
				return interrupted(r, ctx.Err())
			}
			return fail(r, "retrieve_failed", err)
		}
		if got.Done {
			return finish(ctx, p, o, r, job, got, file, path)
		}
		r.Status = got.Status
		if o.Stderr != nil {
			fmt.Fprintf(o.Stderr, "Status: %s\n", got.Status)
		}
		if o.NoWait {
			return r, nil
		}
		timer := time.NewTimer(o.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return interrupted(r, ctx.Err())
		case <-timer.C:
		}
	}
}
func interrupted(r Result, err error) (Result, error) {
	status := "cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timed_out"
	}
	return fail(r, status, fmt.Errorf("video outcome unknown: %w", err))
}

func finish(ctx context.Context, p Retriever, o Options, r Result, job media.QueuedJob, got *media.Retrieval, file *os.File, path string) (Result, error) {
	if err := file.Sync(); err != nil {
		return fail(r, "save_failed", fmt.Errorf("sync video: %w", err))
	}
	stat, err := file.Stat()
	if err != nil {
		return fail(r, "save_failed", err)
	}
	if err := file.Close(); err != nil {
		return fail(r, "save_failed", err)
	}
	if err := atomicfile.Replace(file.Name(), path); err != nil {
		return fail(r, "save_failed", fmt.Errorf("replace video: %w", err))
	}
	r.Status = "completed"
	r.Output = &Output{Path: path, MimeType: got.MimeType, Bytes: stat.Size()}
	if o.DeleteRemote {
		if err := p.(Completer).Complete(ctx, job); err != nil {
			r.Warning = fmt.Sprintf("remote cleanup failed for model=%s queue_id=%s: %v", job.Model, job.QueueID, err)
			if o.Stderr != nil {
				fmt.Fprintln(o.Stderr, "Warning: "+r.Warning)
			}
		}
	}
	return r, nil
}

func prepare(o Options) (*os.File, string, error) {
	path := mediautil.ExpandPath(o.Output)
	if path == "" {
		dir := o.OutputDir
		if dir == "" {
			dir = "~/Pictures/term-llm"
		}
		name := mediautil.SanitizeFilename(o.Request.Prompt)
		if name == "" {
			name = "video"
		}
		if len(name) > 30 {
			name = name[:30]
		}
		path = filepath.Join(mediautil.ExpandPath(dir), time.Now().Format("20060102-150405")+"-"+name+".mp4")
	}
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		return nil, "", fmt.Errorf("output path is a directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, "", fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".term-llm-video-*.tmp")
	if err != nil {
		return nil, "", fmt.Errorf("preflight output: %w", err)
	}
	return file, path, nil
}
func discard(file *os.File) { file.Close(); os.Remove(file.Name()) }

// ValidateMode rejects generation controls in retrieval mode using explicit flag
// presence, so --audio=false and --negative-prompt with an empty value cannot slip through.
func ValidateMode(queueID, model string, hasPrompt bool, changed []string, quoteOnly, skipQuote, noWait bool, poll, timeout time.Duration, queueChanged bool) error {
	if poll <= 0 || timeout <= 0 {
		return fmt.Errorf("poll interval and timeout must be greater than 0")
	}
	if quoteOnly && (skipQuote || noWait) {
		return fmt.Errorf("--quote-only conflicts with --skip-quote or --no-wait")
	}
	if queueChanged && queueID == "" {
		return fmt.Errorf("--queue-id must not be empty")
	}
	if queueID != "" {
		if model == "" {
			return fmt.Errorf("--queue-id requires --model")
		}
		if hasPrompt || len(changed) > 0 {
			return fmt.Errorf("--queue-id rejects prompts and generation options")
		}
	}
	return nil
}

// ValidateInputURLs performs no file reads and is also safe for quote-only mode.
func ValidateInputURLs(paths []string) error {
	for _, path := range paths {
		if mediautil.IsRemoteReference(path) {
			if err := mediautil.ValidateRemoteURL(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// saveWriter distinguishes disk failures from remote retrieval errors.
type saveWriter struct {
	io.Writer
	err error
}

func (w *saveWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

func requestContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return mediautil.WithMediaTimeout(ctx)
}

func queueRejected(err error) bool {
	var status *providerhttp.StatusError
	if !errors.As(err, &status) {
		return false
	}
	switch status.StatusCode {
	case 400, 401, 402, 403, 404, 405, 413, 415, 422:
		return true
	}
	return false
}
func diagnosticReferences(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	refs := make([]string, len(paths))
	for i, path := range paths {
		refs[i] = mediautil.DiagnosticReference(path)
	}
	return refs
}

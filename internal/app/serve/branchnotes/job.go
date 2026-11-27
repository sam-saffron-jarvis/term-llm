package branchnotes

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm/engine"
)

type Job struct {
	mu     sync.Mutex
	status wire.PathNotesStatus
	cancel context.CancelFunc
	done   chan struct{}
}

func (job *Job) Prepared(p engine.PathNotesPrepared) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.status.State == "skipped" {
		return
	}
	now := time.Now()
	job.status.State, job.status.WritingAt, job.status.Model = "writing", &now, p.Model
	job.status.SourceMessages, job.status.OmittedMessages = &p.SourceMessages, p.OmittedMessages
	job.status.ReadFiles, job.status.ModifiedFiles = p.ReadFiles, p.ModifiedFiles
}

func (job *Job) Text(delta string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.status.State != "writing" {
		return
	}
	remaining := 4096 - len(job.status.Preview)
	if len(delta) > remaining {
		for remaining > 0 && !utf8.RuneStart(delta[remaining]) {
			remaining--
		}
		delta = delta[:remaining]
	}
	job.status.Preview += delta
}

func (job *Job) ResetText() {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status.Preview = ""
}

func NewJob(status wire.PathNotesStatus, cancel context.CancelFunc) *Job {
	return &Job{status: status, cancel: cancel, done: make(chan struct{})}
}

func (job *Job) Snapshot() *wire.PathNotesStatus {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.status.State == "skipped" {
		return nil
	}
	snapshot := job.status
	return &snapshot
}

func (job *Job) Skip() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.status.State == "saving" {
		return false
	}
	job.status.State = "skipped"
	if job.cancel != nil {
		job.cancel()
	}
	return true
}

func (job *Job) BeginSave(ctx context.Context) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.status.State == "skipped" || ctx.Err() != nil {
		return false
	}
	job.status.State = "saving"
	return true
}

func (job *Job) Done() <-chan struct{} { return job.done }
func (job *Job) finish()               { close(job.done) }

func (job *Job) Skipped() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.status.State == "skipped"
}

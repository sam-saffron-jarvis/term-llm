package branchnotes

import (
	"sync"
	"time"
)

// A delayed or replayed POST must not undo a skip. This marker is separate
// from active jobs, so it never blocks chat or appears as pending progress.
const skipTTL = 2 * time.Minute

type Registry struct {
	mu    sync.Mutex
	jobs  map[string]*Job
	skips map[string]time.Time
}

func (r *Registry) Load(id string) (*Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	return job, ok
}

// Start returns whether a job was already active or the child was skipped.
func (r *Registry) Start(id string, job *Job) (active, skipped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.skips[id]) {
		return false, true
	}
	if r.jobs[id] != nil {
		return true, false
	}
	if r.jobs == nil {
		r.jobs = make(map[string]*Job)
	}
	r.jobs[id] = job
	return false, false
}

// skip marks a child even before POST registers. A saving job won the race
// and stays registered: its durable result must be checked by the caller.
func (r *Registry) skip(id string) (saving *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job := r.jobs[id]; job != nil && !job.Skip() {
		return job
	}
	delete(r.jobs, id)
	now := time.Now()
	if now.Before(r.skips[id]) {
		return nil
	}
	if r.skips == nil {
		r.skips = make(map[string]time.Time)
	}
	until := now.Add(skipTTL)
	r.skips[id] = until
	time.AfterFunc(skipTTL, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.skips[id] == until {
			delete(r.skips, id)
		}
	})
	return nil
}

func (r *Registry) Finish(id string, job *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs[id] == job {
		delete(r.jobs, id)
	}
	job.finish()
}

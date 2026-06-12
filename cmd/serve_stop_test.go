package cmd

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServeStopHonorsContextWhenResponseRunCloseBlocks(t *testing.T) {
	mgr := newServeResponseRunManager()
	release := make(chan struct{})
	mgr.runWG.Add(1)
	go func() {
		<-release
		mgr.runWG.Done()
	}()

	var releaseOnce sync.Once
	releaseRun := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	defer releaseRun()
	time.AfterFunc(500*time.Millisecond, releaseRun)

	srv := &serveServer{
		server:       &http.Server{},
		shutdownCh:   make(chan struct{}),
		responseRuns: mgr,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := srv.Stop(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want context deadline exceeded", err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("Stop took %s, want it to return before blocked teardown completes", elapsed)
	}
}

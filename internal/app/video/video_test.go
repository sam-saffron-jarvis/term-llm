package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/providerhttp"
	media "github.com/term-llm/term-llm/internal/video"
)

type fakeProvider struct {
	quotes, queues, retrieves, cleans int
	retrieveErr                       error
	pending                           bool
	cleanupErr                        error
	output                            string
	t                                 *testing.T
	queueErr                          error
	checkContext                      func(context.Context)
}

func (p *fakeProvider) Quote(ctx context.Context, _ media.Request) (*media.Quote, error) {
	if p.checkContext != nil {
		p.checkContext(ctx)
	}
	p.quotes++
	return &media.Quote{Amount: 1}, nil
}
func (p *fakeProvider) Queue(ctx context.Context, _ media.Request) (*media.QueuedJob, error) {
	if p.checkContext != nil {
		p.checkContext(ctx)
	}
	p.queues++
	if p.queueErr != nil {
		return nil, p.queueErr
	}
	return &media.QueuedJob{Model: "model", QueueID: "job"}, nil
}
func (p *fakeProvider) RetrieveTo(ctx context.Context, _ media.QueuedJob, w io.Writer, _ bool) (*media.Retrieval, error) {
	if p.checkContext != nil {
		p.checkContext(ctx)
	}
	p.retrieves++
	if p.retrieveErr != nil {
		return nil, p.retrieveErr
	}
	if p.pending {
		return &media.Retrieval{Status: "NEW_UNKNOWN_STATUS"}, nil
	}
	_, err := w.Write([]byte("video-fixture"))
	return &media.Retrieval{Done: true, MimeType: "video/mp4"}, err
}
func (p *fakeProvider) Complete(context.Context, media.QueuedJob) error {
	p.cleans++
	data, err := os.ReadFile(p.output)
	if err != nil || string(data) != "video-fixture" {
		p.t.Fatalf("cleanup before local save: %q %v", data, err)
	}
	return p.cleanupErr
}
func options(t *testing.T) Options {
	return Options{Request: media.Request{Model: "model", Prompt: "hello", Duration: "5s"}, Output: filepath.Join(t.TempDir(), "video.mp4"), PollInterval: time.Nanosecond, Timeout: time.Minute}
}
func TestGenerateReceiptAndSafeSave(t *testing.T) {
	for _, del := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "delete"}[del], func(t *testing.T) {
			o := options(t)
			var stderr bytes.Buffer
			o.Stderr = &stderr
			o.DeleteRemote = del
			p := &fakeProvider{output: o.Output, t: t}
			r, err := Generate(context.Background(), p, o)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "completed" || !strings.Contains(stderr.String(), "queue_id=job") || r.Recovery == "" {
				t.Fatalf("result %+v stderr %s", r, stderr.String())
			}
			if p.cleans != map[bool]int{false: 0, true: 1}[del] {
				t.Fatalf("cleanup %d", p.cleans)
			}
			matches, _ := filepath.Glob(filepath.Join(filepath.Dir(o.Output), "*.tmp"))
			if len(matches) > 0 {
				t.Fatal(matches)
			}
		})
	}
}
func TestQuoteOnlyDoesNotReadOrSave(t *testing.T) {
	o := options(t)
	o.QuoteOnly = true
	o.Output = "/nonexistent/not-used"
	o.LoadInputs = func(*media.Request) error { t.Fatal("read inputs"); return nil }
	p := &fakeProvider{}
	r, err := Generate(context.Background(), p, o)
	if err != nil || r.Status != "quoted" || p.queues != 0 || p.retrieves != 0 {
		t.Fatalf("%+v %v %+v", r, err, p)
	}
}
func TestSkipQuoteQueueOnly(t *testing.T) {
	o := options(t)
	o.SkipQuote = true
	o.NoWait = true
	var receipt bytes.Buffer
	o.Stderr = &receipt
	p := &fakeProvider{}
	r, err := Generate(context.Background(), p, o)
	if err != nil || p.quotes != 0 || p.queues != 1 || p.retrieves != 0 || r.Job == nil || receipt.Len() == 0 {
		t.Fatalf("%+v %v %+v", r, err, p)
	}
}
func TestRetrieveOnlyNeverQueuesAndRecoverErrors(t *testing.T) {
	for _, kind := range []string{"pending", "failure", "cancel", "timeout", "success"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			o.QueueID = "job"
			o.LoadInputs = func(*media.Request) error { t.Fatal("read inputs"); return nil }
			p := &fakeProvider{}
			ctx := context.Background()
			switch kind {
			case "pending":
				p.pending = true
				o.NoWait = true
			case "failure":
				p.retrieveErr = errors.New("offline")
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "timeout":
				c, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = c
			}
			r, err := RetrieveExisting(ctx, p, o)
			if p.quotes != 0 || p.queues != 0 || r.Job == nil || r.Recovery == "" {
				t.Fatalf("%+v %+v", r, p)
			}
			if (kind == "failure" || kind == "cancel" || kind == "timeout") && err == nil {
				t.Fatal("missing error")
			}
		})
	}
}
func TestPreflightBeforeSpend(t *testing.T) {
	o := options(t)
	o.Output = t.TempDir()
	p := &fakeProvider{}
	_, err := Generate(context.Background(), p, o)
	if err == nil || p.quotes != 0 || p.queues != 0 || p.retrieves != 0 {
		t.Fatalf("%v %+v", err, p)
	}
}
func TestSaveFailureDoesNotCleanup(t *testing.T) {
	o := options(t)
	p := &fakeProvider{t: t, output: o.Output}
	o.DeleteRemote = true
	o.LoadInputs = func(*media.Request) error { return os.Mkdir(o.Output, 0700) }
	r, err := Generate(context.Background(), p, o)
	if err == nil || r.Status != "save_failed" || r.Job == nil || p.cleans != 0 {
		t.Fatalf("%+v %v %+v", r, err, p)
	}
}
func TestCleanupFailureIsWarning(t *testing.T) {
	o := options(t)
	o.DeleteRemote = true
	p := &fakeProvider{t: t, output: o.Output, cleanupErr: errors.New("offline")}
	r, err := Generate(context.Background(), p, o)
	if err != nil || r.Status != "completed" || r.Warning == "" {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestModePresenceConflicts(t *testing.T) {
	for _, flag := range []string{"audio", "negative-prompt", "resolution", "input", "quote-only", "seedance-consent"} {
		if err := ValidateMode("id", "model", false, []string{flag}, false, false, false, time.Second, time.Minute, true); err == nil {
			t.Errorf("accepted explicit %s", flag)
		}
	}
}

func TestQueueFailureNeverRetriesOrSuggestsRequeue(t *testing.T) {
	o := options(t)
	o.SkipQuote = true
	p := &fakeProvider{queueErr: context.DeadlineExceeded}
	r, err := Generate(context.Background(), p, o)
	if err == nil || p.queues != 1 || p.retrieves != 0 || r.Job != nil || r.Status != "queue_outcome_unknown" || !strings.Contains(err.Error(), "do not requeue") {
		t.Fatalf("%+v %v %+v", r, err, p)
	}
}

func TestVideoOverallBudgetDefaultAndOverrides(t *testing.T) {
	for _, retrieve := range []bool{false, true} {
		for _, timeout := range []time.Duration{0, 2 * time.Hour, 10 * time.Second} {
			t.Run(fmt.Sprintf("retrieve=%t/%s", retrieve, timeout), func(t *testing.T) {
				o := options(t)
				o.Timeout = timeout
				var first time.Time
				p := &fakeProvider{checkContext: func(ctx context.Context) {
					deadline, ok := ctx.Deadline()
					if !ok {
						t.Error("missing overall deadline")
					}
					if first.IsZero() {
						first = deadline
						want := timeout
						if want == 0 {
							want = time.Hour
						}
						remaining := time.Until(deadline)
						if remaining > want || remaining < want-time.Second {
							t.Errorf("overall=%s want=%s", remaining, want)
						}
					} else if !first.Equal(deadline) {
						t.Error("nested timer shortened overall budget")
					}
				}}
				var err error
				if retrieve {
					o.QueueID = "job"
					_, err = RetrieveExisting(context.Background(), p, o)
				} else {
					_, err = Generate(context.Background(), p, o)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestVideoOverallDefaultPreservesLongParent(t *testing.T) {
	o := options(t)
	o.Timeout = 0
	o.NoWait = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	want, _ := ctx.Deadline()
	p := &fakeProvider{checkContext: func(ctx context.Context) {
		got, _ := ctx.Deadline()
		if !got.Equal(want) {
			t.Error("default shortened parent")
		}
	}}
	if _, err := Generate(ctx, p, o); err != nil {
		t.Fatal(err)
	}
}
func TestVideoInvalidRequestNeverPreflightsOrSpends(t *testing.T) {
	for _, kind := range []string{"duration", "resolution", "aspect", "delete-no-wait"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			o.SkipQuote = true
			o.Output = filepath.Join(t.TempDir(), "not-created", "video.mp4")
			switch kind {
			case "duration":
				o.Request.Duration = "5"
			case "resolution":
				v := "bad"
				o.Request.Resolution = &v
			case "aspect":
				o.Request.AspectRatio = "0:1"
			case "delete-no-wait":
				o.DeleteRemote = true
				o.NoWait = true
			}
			p := &fakeProvider{}
			r, err := Generate(context.Background(), p, o)
			if err == nil || r.Status != "invalid" || p.quotes != 0 || p.queues != 0 || p.retrieves != 0 || strings.Contains(err.Error(), "do not requeue") {
				t.Fatalf("%+v %v %+v", r, err, p)
			}
			if _, err := os.Stat(filepath.Dir(o.Output)); !os.IsNotExist(err) {
				t.Fatal("invalid request preflighted output", err)
			}
		})
	}
}
func TestQueueDefiniteRejectionIsNotUnknown(t *testing.T) {
	for _, status := range []int{400, 401, 402, 422} {
		o := options(t)
		o.SkipQuote = true
		p := &fakeProvider{queueErr: &providerhttp.StatusError{StatusCode: status}}
		r, err := Generate(context.Background(), p, o)
		if err == nil || r.Status != "queue_rejected" || p.queues != 1 || strings.Contains(err.Error(), "do not requeue") {
			t.Fatal(r, err, p)
		}
	}
}

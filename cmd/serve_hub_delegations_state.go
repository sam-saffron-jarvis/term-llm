package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/samsaffron/term-llm/internal/hub"
)

// checkDelegationCaps enforces hub-wide, per-origin, and per-target in-flight
// limits. Ledger statuses only advance when polled, so before rejecting it
// refreshes active records once (bounded by the hub-wide cap) and recounts.
func (s *hubServer) checkDelegationCaps(ctx context.Context, origin, target hub.Node) error {
	over := func() (error, bool) {
		total, byOrigin, byTarget, err := s.delegations.ActiveCounts()
		if err != nil {
			return err, false
		}
		if byTarget[target.ID] >= target.DelegationMaxInFlight() {
			return fmt.Errorf("node %q already has %d delegations in flight (max %d)", target.ID, byTarget[target.ID], target.DelegationMaxInFlight()), true
		}
		if byOrigin[origin.ID] >= hubDelegationOriginMaxInFlight {
			return fmt.Errorf("node %q already originates %d delegations in flight (max %d)", origin.ID, byOrigin[origin.ID], hubDelegationOriginMaxInFlight), true
		}
		if total >= hubDelegationHubMaxInFlight {
			return fmt.Errorf("hub already has %d delegations in flight (max %d)", total, hubDelegationHubMaxInFlight), true
		}
		return nil, false
	}
	err, capped := over()
	if err == nil || !capped {
		return err
	}
	s.refreshActiveDelegations(ctx)
	err, _ = over()
	return err
}

// refreshActiveDelegations re-polls every active record's target run so stale
// "running" entries don't pin in-flight caps forever.
func (s *hubServer) refreshActiveDelegations(ctx context.Context) {
	records, err := s.delegations.List()
	if err != nil {
		return
	}
	for i := range records {
		if hub.DelegationStatusTerminal(records[i].Status) {
			continue
		}
		_ = s.refreshDelegation(ctx, &records[i])
	}
}

// refreshDelegation polls the target node for the delegation's latest run and
// folds any status change back into the ledger and *d.
func (s *hubServer) refreshDelegation(ctx context.Context, d *hub.Delegation) error {
	if hub.DelegationStatusTerminal(d.Status) || d.JobID == "" {
		return nil
	}
	target, ok := s.registry.Lookup(d.TargetNode)
	if !ok {
		return fmt.Errorf("target node %q is not present in the hub registry", d.TargetNode)
	}
	var run hubNodeJobsRun
	if d.RunID != "" {
		// Poll the exact run the delegation triggered: the job's newest run
		// could be a different (e.g. manually re-triggered) one.
		if err := s.doNodeJSON(ctx, target, http.MethodGet,
			"/v2/runs/"+url.PathEscape(d.RunID), nil, &run); err != nil {
			return fmt.Errorf("poll node %q: %w", target.ID, err)
		}
	} else {
		var runs struct {
			Data []hubNodeJobsRun `json:"data"`
		}
		if err := s.doNodeJSON(ctx, target, http.MethodGet,
			"/v2/runs?limit=1&offset=0&job_id="+url.QueryEscape(d.JobID), nil, &runs); err != nil {
			return fmt.Errorf("poll node %q: %w", target.ID, err)
		}
		if len(runs.Data) == 0 {
			return nil
		}
		run = runs.Data[0]
	}
	status := delegationStatusFromRun(run.Status)
	if status == d.Status && run.ID == d.RunID {
		return nil
	}
	updated, err := s.delegations.Update(d.ID, func(rec *hub.Delegation) {
		rec.Status = status
		if run.ID != "" {
			rec.RunID = run.ID
		}
		rec.Error = ""
		if hub.DelegationStatusTerminal(status) {
			rec.Response = run.Response
			rec.Error = run.Error
		}
	})
	if err != nil {
		return err
	}
	*d = updated
	return nil
}

// delegationStatusFromRun maps a jobs-v2 run status onto a delegation status.
func delegationStatusFromRun(runStatus string) string {
	switch runStatus {
	case "succeeded":
		return hub.DelegationStatusSucceeded
	case "failed", "skipped":
		return hub.DelegationStatusFailed
	case "cancelled":
		return hub.DelegationStatusCancelled
	case "cancel_requested":
		return hub.DelegationStatusCancelRequested
	case "timed_out":
		return hub.DelegationStatusTimedOut
	default:
		return hub.DelegationStatusRunning
	}
}

// resolveDelegationCwd defaults to the target's delegation workdir and
// confines an explicit cwd inside it: the workdir is the target operator's
// consent boundary for delegated work. The workdir itself is canonicalized
// (absolute, "."/".." resolved) so prefix containment cannot be confused by
// an un-normalized configured path.
func resolveDelegationCwd(workdir, requested string) (string, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return "", errors.New("target node has no delegation workdir")
	}
	if hasControlRune(workdir) {
		return "", fmt.Errorf("target node's delegation workdir contains control characters")
	}
	if hasControlRune(requested) {
		return "", fmt.Errorf("cwd contains control characters")
	}
	if !strings.HasPrefix(workdir, "/") {
		return "", fmt.Errorf("target node's delegation workdir %q is not an absolute path", workdir)
	}
	workdir = pathClean(workdir)
	if workdir == "" {
		return "", errors.New("target node's delegation workdir resolves to the filesystem root; refusing")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return workdir, nil
	}
	if !strings.HasPrefix(requested, "/") {
		requested = workdir + "/" + requested
	}
	cwd := pathClean(requested)
	if cwd == workdir || strings.HasPrefix(cwd, workdir+"/") {
		return cwd, nil
	}
	return "", fmt.Errorf("cwd %q is outside the target node's delegation workdir %q", requested, workdir)
}

func hasControlRune(s string) bool {
	for _, r := range s {
		if r == 0 || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// pathClean is filepath.Clean with forward slashes: delegation workdirs are
// paths on the (POSIX) target node, not on the hub host.
func pathClean(p string) string {
	cleaned := strings.Builder{}
	segments := strings.Split(p, "/")
	stack := make([]string, 0, len(segments))
	for _, seg := range segments {
		switch seg {
		case "", ".":
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, seg)
		}
	}
	cleaned.WriteString("/" + strings.Join(stack, "/"))
	return strings.TrimRight(cleaned.String(), "/")
}

// hubDelegationInstructions returns the delegated prompt as the target node's
// user-visible task text. Delegation provenance is trusted metadata carried in
// hub-written job labels and tool context; it should not pollute the target
// session transcript or change what the origin asked the target to do.
func hubDelegationInstructions(_ hub.Delegation, prompt string) string {
	return prompt
}

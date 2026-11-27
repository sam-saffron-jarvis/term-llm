package serve

import "context"

func (s *serveServer) claimResponsePreparation(ctx context.Context, rr resolvedResponsesRequest, sessionID, scope, key string) (func(), error) {
	releaseIdempotency := func() {}
	if rr.req.Stream && key != "" {
		var err error
		releaseIdempotency, err = s.ensureResponseRuns().admitIdempotency(ctx, scope, key)
		if err != nil {
			return nil, err
		}
	}
	if !rr.freshConversation || !s.projectsEnabled || sessionID == "" {
		return releaseIdempotency, nil
	}
	// Serialize fresh project-mode requests from workspace resolution through
	// run admission. Otherwise a competing request can replace the installed
	// runtime (or contend for its operation lock) before its own binding fails,
	// leaving the request that actually bound the workspace unable to run.
	releaseFresh, err := s.ensureResponseRuns().freshAdmissions.Acquire(ctx, sessionID)
	if err != nil {
		releaseIdempotency()
		return nil, err
	}
	return func() {
		releaseFresh()
		releaseIdempotency()
	}, nil
}

package video

import (
	"fmt"

	"github.com/term-llm/term-llm/internal/mediautil"
	"github.com/term-llm/term-llm/internal/venice"
)

// RequestNotSentError distinguishes definite local validation from uncertain
// paid transport outcomes. It never means a request was accepted upstream.
type RequestNotSentError struct{ Err error }

func (e *RequestNotSentError) Error() string { return "video request not sent: " + e.Err.Error() }
func (e *RequestNotSentError) Unwrap() error { return e.Err }

// ValidateRequest checks structure without reading files or making requests.
func ValidateRequest(req Request) error {
	if err := validateRequest(req); err != nil {
		return &RequestNotSentError{err}
	}
	return nil
}
func validateRequest(req Request) error {
	if !validIdentifier(req.Model) {
		return fmt.Errorf("model is required and must be a valid identifier")
	}
	duration := req.Duration
	if duration == "" {
		duration = DefaultDuration
	}
	if err := ValidateDuration(duration); err != nil {
		return err
	}
	if req.Resolution != nil {
		if err := ValidateResolution(*req.Resolution); err != nil {
			return err
		}
	}
	if err := ValidateAspectRatio(req.AspectRatio); err != nil {
		return err
	}
	if req.ImageURL != "" {
		if len(req.ImageData) > 0 {
			return fmt.Errorf("image URL and local bytes conflict")
		}
		if err := mediautil.ValidateRemoteURL(req.ImageURL); err != nil {
			return err
		}
	}
	for _, ref := range req.ReferenceImages {
		if ref.URL != "" {
			if len(ref.Data) > 0 {
				return fmt.Errorf("reference URL and local bytes conflict")
			}
			if err := mediautil.ValidateRemoteURL(ref.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateRequest includes only fresh root-scoped cached constraints; it never
// discovers models or reads original image files.
func (p *VeniceProvider) ValidateRequest(req Request) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}
	duration := req.Duration
	if duration == "" {
		duration = DefaultDuration
	}
	if err := venice.ValidateVideoOptions(p.baseURL, req.Model, duration, req.Resolution); err != nil {
		return &RequestNotSentError{err}
	}
	return nil
}

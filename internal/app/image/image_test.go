package image

import (
	"context"
	"strings"
	"testing"
	"time"

	media "github.com/term-llm/term-llm/internal/image"
)

func TestOptionCombinationsBeforeReads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		o      Options
		prompt bool
	}{
		{"processing prompt", Options{Operation: "upscale", Inputs: []string{"x.png"}}, true},
		{"processing remote", Options{Operation: "upscale", Inputs: []string{"https://example.org/x"}}, false},
		{"processing stdin", Options{Operation: "background-remove", Inputs: []string{"-"}}, false},
		{"processing sizing", Options{Operation: "upscale", Inputs: []string{"x.png"}, Changed: []string{"size"}}, false},
		{"edit generation controls", Options{Inputs: []string{"x.png"}, Changed: []string{"safe-mode"}}, true},
		{"upscale flags on generate", Options{Changed: []string{"enhance"}}, true},
		{"editor on generate", Options{EditModel: "editor", Changed: []string{"edit-model"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.o.Timeout = time.Minute
			if err := tc.o.Validate(tc.prompt); err == nil {
				t.Fatal("invalid combination accepted")
			}
		})
	}
}
func TestUnsupportedCapabilitiesBeforeReads(t *testing.T) {
	steps := 5
	for _, o := range []Options{
		{Size: media.NativeSize, Inputs: []string{"file.png"}},
		{Controls: media.VeniceControls{Steps: &steps}, Changed: []string{"steps"}},
		{Operation: "upscale", Inputs: []string{"file.png"}},
		{Inputs: []string{"file.png", "https://example.org/image"}},
		{EditModel: "editor", Inputs: []string{"file.png"}, Changed: []string{"edit-model"}},
	} {
		o.Timeout = time.Minute
		_, _, err := o.Prepare(media.NewDebugProvider(0), "", func(string) ([]byte, error) { t.Fatal("local read before capability gate"); return nil, nil })
		if err == nil {
			t.Fatal("unsupported capability accepted")
		}
	}
}
func TestProcessingOutputName(t *testing.T) {
	o := Options{Operation: "background-remove", Inputs: []string{"dir/photo.jpg"}}
	if got := o.OutputName(); got != "photo-background-remove" {
		t.Fatal(got)
	}
}
func TestLegacyDebugGenerationStillWorks(t *testing.T) {
	o := Options{Timeout: time.Minute}
	label, run, err := o.Prepare(media.NewDebugProvider(0), "hello", nil)
	if err != nil || !strings.Contains(label, "Generating") {
		t.Fatal(label, err)
	}
	if result, err := run(context.Background()); err != nil || len(result.Data) == 0 {
		t.Fatal(err)
	}
}

package branchnotes

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
)

func TestPathNoteSaveAndSkipAreExclusive(t *testing.T) {
	for _, saveFirst := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		job := NewJob(wire.PathNotesStatus{State: "writing"}, cancel)
		job.Text(strings.Repeat("界", 2000))
		if got := job.Snapshot().Preview; len(got) > 4096 || !utf8.ValidString(got) {
			t.Fatal("preview exceeded cap or split rune")
		}
		if saveFirst {
			if !job.BeginSave(ctx) || job.Skip() || ctx.Err() != nil || job.Snapshot().State != "saving" {
				t.Fatal("skip interrupted save")
			}
		} else if !job.Skip() || job.BeginSave(ctx) || ctx.Err() == nil || job.Snapshot() != nil {
			t.Fatal("save overrode skip")
		}
		cancel()
	}
}

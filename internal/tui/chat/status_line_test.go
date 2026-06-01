package chat

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/ui"
)

func TestRenderStatusLineShowsConfiguredContextWindow(t *testing.T) {
	llm.RegisterConfigLimits([]llm.ConfigModelLimit{{Provider: "mock", Model: "mock-model", InputLimit: 1048576}})
	defer llm.RegisterConfigLimits(nil)

	m := newTestChatModel(false)
	m.width = 120
	m.engine.ConfigureContextManagement(m.provider, m.providerKey, m.modelName, false)

	line := ui.StripANSI(m.renderStatusLine())
	if !strings.Contains(line, "~0/1M") {
		t.Fatalf("status line %q does not show context window", line)
	}
}

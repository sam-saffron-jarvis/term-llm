package termimage

import (
	"fmt"
	"os"
	"strings"
)

const (
	envImageProtocol  = "TERM_LLM_IMAGE_PROTOCOL"
	envImageDebug     = "TERM_LLM_IMAGE_DEBUG"
	envImageDebugFile = "TERM_LLM_IMAGE_DEBUG_FILE"
)

// DefaultEnvironment returns terminal-image relevant environment variables.
func DefaultEnvironment() Environment {
	return Environment{
		Term:           os.Getenv("TERM"),
		TermProgram:    os.Getenv("TERM_PROGRAM"),
		LCTerminal:     os.Getenv("LC_TERMINAL"),
		KittyWindowID:  os.Getenv("KITTY_WINDOW_ID"),
		ColorTerm:      os.Getenv("COLORTERM"),
		Tmux:           os.Getenv("TMUX"),
		Screen:         os.Getenv("STY"),
		ForcedProtocol: os.Getenv(envImageProtocol),
		Debug:          truthyEnv(os.Getenv(envImageDebug)),
		DebugFile:      os.Getenv(envImageDebugFile),
	}
}

func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

func normalizeProtocol(p Protocol) Protocol {
	switch Protocol(strings.ToLower(strings.TrimSpace(string(p)))) {
	case "", ProtocolAuto:
		return ProtocolAuto
	case ProtocolKitty:
		return ProtocolKitty
	case ProtocolITerm, "iterm2", "iterm2-inline":
		return ProtocolITerm
	case ProtocolSixel:
		return ProtocolSixel
	case ProtocolANSI, "text", "halfblock", "half-block":
		return ProtocolANSI
	case ProtocolNone, "off", "disabled", "caption":
		return ProtocolNone
	default:
		return ProtocolAuto
	}
}

func normalizeMode(m Mode) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(string(m)))) {
	case ModeViewport:
		return ModeViewport
	case ModeOneShot:
		return ModeOneShot
	case "", ModeScrollback:
		return ModeScrollback
	default:
		return ModeScrollback
	}
}

// Select chooses the rendering strategy for a request and environment.
func Select(req Request, env Environment) Strategy {
	mode := normalizeMode(req.Mode)
	requested := normalizeProtocol(req.Protocol)
	forced := normalizeProtocol(Protocol(env.ForcedProtocol))
	if forced != ProtocolAuto {
		return selectRequestedProtocol(mode, forced, true)
	}
	if requested != ProtocolAuto {
		return selectRequestedProtocol(mode, requested, false)
	}

	caps := detectCapabilities(env)
	if caps.tmux && caps.kitty {
		caps.kitty = false
	}
	if mode == ModeViewport {
		// Yazi-style adapter selection: choose the most reliable adapter for the
		// rendering surface, not just the richest protocol advertised by the terminal.
		// Bubble Tea owns and diffs viewport text, while Kitty Unicode placeholders are
		// composited by the terminal from that text. Resizes/scrolling can desync those
		// two renderers, so keep Kitty placeholders as an explicit opt-in for chat TUI
		// viewports and use ANSI text by default.
		return Strategy{Protocol: ProtocolANSI, Name: "ansi-viewport-stable"}
	}

	if caps.kitty {
		return Strategy{Protocol: ProtocolKitty, Name: "kitty-placeholder"}
	}
	if caps.iterm {
		return Strategy{Protocol: ProtocolITerm, Name: "iterm-inline"}
	}
	if caps.sixel {
		return Strategy{Protocol: ProtocolSixel, Name: "sixel"}
	}
	return Strategy{Protocol: ProtocolANSI, Name: "ansi-fallback"}
}

func selectRequestedProtocol(mode Mode, protocol Protocol, forced bool) Strategy {
	nameSuffix := "requested"
	if forced {
		nameSuffix = "forced"
	}
	if mode == ModeViewport {
		switch protocol {
		case ProtocolKitty, ProtocolANSI, ProtocolNone:
			return Strategy{Protocol: protocol, Name: string(protocol) + "-" + nameSuffix}
		case ProtocolITerm, ProtocolSixel:
			return Strategy{
				Protocol: ProtocolANSI,
				Name:     "ansi-viewport-fallback",
				Warnings: []string{fmt.Sprintf("%s is not safe inside a redrawable viewport; using ansi", protocol)},
			}
		}
	}
	return Strategy{Protocol: protocol, Name: string(protocol) + "-" + nameSuffix}
}

type capabilities struct {
	kitty bool
	iterm bool
	sixel bool
	tmux  bool
}

func detectCapabilities(env Environment) capabilities {
	term := strings.ToLower(env.Term)
	termProgram := strings.ToLower(env.TermProgram)
	lcTerminal := strings.ToLower(env.LCTerminal)

	var caps capabilities
	caps.tmux = strings.TrimSpace(env.Tmux) != ""
	if strings.TrimSpace(env.KittyWindowID) != "" || strings.Contains(term, "kitty") || termProgram == "kitty" || termProgram == "ghostty" || strings.Contains(termProgram, "ghostty") {
		caps.kitty = true
	}
	if termProgram == "iterm.app" || lcTerminal == "iterm2" || termProgram == "wezterm" || strings.Contains(termProgram, "wezterm") {
		caps.iterm = true
	}
	if strings.Contains(term, "sixel") || strings.Contains(term, "mlterm") || strings.Contains(term, "foot") || strings.Contains(termProgram, "rio") {
		caps.sixel = true
	}
	return caps
}

func Debugf(env Environment, format string, args ...any) {
	debugf(env, format, args...)
}

func debugf(env Environment, format string, args ...any) {
	if !env.Debug {
		return
	}
	line := fmt.Sprintf("[term-llm image] "+format+"\n", args...)
	if path := strings.TrimSpace(env.DebugFile); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
			return
		}
	}
	fmt.Fprint(os.Stderr, line)
}

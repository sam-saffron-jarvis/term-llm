package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	codemodetool "github.com/term-llm/term-llm/internal/codemode/tool"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/skills"
)

// ActivateSkillToolName is the tool spec name.
const ActivateSkillToolName = "activate_skill"

// ActivateSkillArgs are the arguments for the activate_skill tool.
type ActivateSkillArgs struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt,omitempty"`
}

// SkillActivatedCallback is called after a skill is activated. The presence bit
// distinguishes an omitted allowlist (inherit all runtime tools) from an
// explicitly empty allowlist (allow no callable tools).
type SkillActivatedCallback func(allowedTools []string, present bool)

// SkillToolsRegisteredCallback is called when a skill with declared tools is activated.
// It receives the skill's tool definitions and the skill's source directory,
// allowing the caller to register the tools with the engine dynamically.
type SkillToolsRegisteredCallback func(defs []skills.SkillToolDef, skillDir string)

// ActivateSkillTool implements the activate_skill tool.
type ActivateSkillTool struct {
	registry         *skills.Registry
	approval         *ApprovalManager
	onActivated      SkillActivatedCallback
	onToolsActivated SkillToolsRegisteredCallback
	loaded           sync.Map // session ID -> *sync.Map of skill name -> skillContext
	codemodetool.ActivationAdmission
}

type skillContext struct {
	body     string
	userTurn int
}

// NewActivateSkillTool creates a new activate_skill tool.
func NewActivateSkillTool(registry *skills.Registry, approval *ApprovalManager) *ActivateSkillTool {
	return &ActivateSkillTool{
		registry: registry,
		approval: approval,
	}
}

// SetOnActivated sets a callback that's called when a skill is activated.
func (t *ActivateSkillTool) SetOnActivated(cb SkillActivatedCallback) {
	t.onActivated = cb
}

// SetOnToolsActivated sets a callback that's called when a skill with declared tools is activated.
// The callback receives the tool definitions and the skill's directory so the caller
// can register them with the engine.
func (t *ActivateSkillTool) SetOnToolsActivated(cb SkillToolsRegisteredCallback) {
	t.onToolsActivated = cb
}

// Spec returns the tool specification.
func (t *ActivateSkillTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: ActivateSkillToolName,
		Description: `Activate a skill to load its specialized instructions and capabilities.
Skills provide domain-specific knowledge and guidelines for completing tasks.

Usage:
- Use the skill name from the <available_skills> list
- Optionally provide a prompt for task-specific context
- The skill's full instructions will be loaded and returned
- Use bundled resources (references/, scripts/, assets/) as needed`,
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The skill name to activate (from available_skills list)",
				},
				"prompt": map[string]interface{}{
					"type":        "string",
					"description": "Optional task-specific context to include with the skill",
				},
			},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
	}
}

// Execute runs the activate_skill tool.
func (t *ActivateSkillTool) Execute(ctx context.Context, args json.RawMessage) (core.ToolOutput, error) {
	var a ActivateSkillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return core.TextOutput(t.formatError(ErrInvalidParams, fmt.Sprintf("failed to parse arguments: %v", err))), nil
	}

	// Validate skill name
	if a.Name == "" {
		return core.TextOutput(t.formatError(ErrInvalidParams, "skill name is required")), nil
	}

	activation, err := skills.NewActivator(t.registry).Activate(skills.ActivationRequest{
		Name:   a.Name,
		Origin: skills.SkillActivationModel,
	})
	if err != nil {
		var activationErr *skills.ActivationError
		if errors.As(err, &activationErr) {
			switch activationErr.Kind {
			case skills.ActivationNotFound:
				return core.TextOutput(t.formatError(ErrFileNotFound, activationErr.Error())), nil
			case skills.ActivationDisabledForOrigin:
				return core.TextOutput(t.formatError(ErrPermissionDenied, activationErr.Error())), nil
			default:
				return core.TextOutput(t.formatError(ErrInvalidParams, activationErr.Error())), nil
			}
		}
		return core.TextOutput(t.formatError(ErrExecutionFailed, err.Error())), nil
	}

	return t.finishActivation(ctx, activation, a.Prompt, true)
}

func (t *ActivateSkillTool) finishActivation(ctx context.Context, activation *skills.Activation, prompt string, model bool) (core.ToolOutput, error) {
	// Register skill-declared tools first so a present allowed-tools filter can
	// include the validated tools it declares.
	if t.onToolsActivated != nil && len(activation.ToolDefs) > 0 {
		t.onToolsActivated(activation.ToolDefs, activation.BaseDir)
	}
	admitted, err := codemodetool.Admitted(t.AdmitCodeModule(activation)) // no code mode: activate without it
	if err != nil {
		return core.ToolOutput{}, err
	}

	// Notify on every activation so an omitted allowlist clears a restriction
	// left by the prior model-activated skill. Explicit empty remains restrictive.
	if model && t.onActivated != nil {
		t.onActivated(activation.AllowedTools, activation.AllowedToolsPresent)
	}

	response := codemodetool.ActivationInstructions(activation, prompt, admitted)
	t.skillInstructions(core.SessionIDFromContext(ctx)).Store(activation.Skill.Name, skillContext{body: response})
	return core.TextOutput(response), nil
}

// Preview returns a short description of the tool call.
func (t *ActivateSkillTool) Preview(args json.RawMessage) string {
	var a ActivateSkillArgs
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return ""
	}
	return fmt.Sprintf("Activating skill: %s", a.Name)
}

// formatError formats an error for the LLM.
func (t *ActivateSkillTool) formatError(errType ToolErrorType, message string) string {
	payload := ToolPayload{Output: message, Error: NewToolError(errType, message)}
	return payload.ToJSON()
}

func (t *ActivateSkillTool) skillInstructions(sessionID string) *sync.Map {
	loaded, _ := t.loaded.LoadOrStore(sessionID, &sync.Map{})
	return loaded.(*sync.Map)
}

// PrepareRequestContext preloads inline references, not leading slash invocations.
// Bodies are request-only: restore them when absent, without repeating admission.
func (t *ActivateSkillTool) PrepareRequestContext(ctx context.Context, sessionID string, messages []core.Message) ([]core.Message, error) {
	if strings.TrimSpace(sessionID) == "" {
		t.ResetSessionState(sessionID)
	}
	loaded := t.skillInstructions(sessionID)
	names, present := skills.InlineSkillContext(messages)
	userTurn := 0
	for _, message := range messages {
		if message.Role == core.RoleUser {
			userTurn++
		}
	}
	for _, name := range names {
		if _, seen := loaded.Load(name); seen {
			continue
		}
		activation, err := skills.NewActivator(t.registry).Preload(name)
		if err == nil {
			if output, err := t.finishActivation(core.ContextWithSessionID(ctx, sessionID), activation, "", false); err == nil {
				loaded.Store(name, skillContext{body: output.Content, userTurn: userTurn})
			}
		}
	}
	names = names[:0]
	loaded.Range(func(name, _ any) bool {
		names = append(names, name.(string))
		return true
	})
	sort.Strings(names)
	for _, name := range names {
		cached, _ := loaded.Load(name)
		context := cached.(skillContext)
		if !strings.Contains(present, context.body) {
			messages = core.InsertRequestContext(messages, core.Message{Role: core.RoleDeveloper, Parts: []core.Part{{Type: core.PartText, Text: context.body}}}, context.userTurn)
		}
	}
	return messages, nil
}

// PrepareCompactionContext forgets auto-load dedup at a known context boundary.
func (t *ActivateSkillTool) PrepareCompactionContext(_ context.Context, sessionID string, _ *engine.CompactionResult) error {
	t.ResetSessionState(sessionID)
	return nil
}

// ResetSessionState also handles out-of-band transcript changes such as undo.
func (t *ActivateSkillTool) ResetSessionState(sessionID string) {
	t.loaded.Delete(sessionID)
}

package skills

import (
	"reflect"
	"strings"
	"testing"
)

func TestInvocationFor(t *testing.T) {
	tests := []struct {
		name    string
		extras  map[string]any
		want    InvocationMetadata
		wantErr string
	}{
		{
			name: "defaults",
			want: InvocationMetadata{
				UserInvocable: true,
				Execution:     SkillExecutionMain,
			},
		},
		{
			name: "all extensions",
			extras: map[string]any{
				"user-invocable":           false,
				"disable-model-invocation": true,
				"argument-hint":            "  [scope]  ",
				"context":                  " fork ",
				"agent":                    " reviewer ",
				"model":                    " fast ",
				"unknown":                  map[string]any{"preserved": true},
			},
			want: InvocationMetadata{
				UserInvocable:          false,
				DisableModelInvocation: true,
				ArgumentHint:           "[scope]",
				Execution:              SkillExecutionIsolatedAgent,
				Agent:                  "reviewer",
				Model:                  "fast",
			},
		},
		{
			name:   "fork defaults to developer",
			extras: map[string]any{"context": "fork"},
			want: InvocationMetadata{
				UserInvocable: true,
				Execution:     SkillExecutionIsolatedAgent,
				Agent:         "developer",
			},
		},
		{
			name:   "explicit main",
			extras: map[string]any{"context": "main", "agent": "ignored-until-execution"},
			want: InvocationMetadata{
				UserInvocable: true,
				Execution:     SkillExecutionMain,
				Agent:         "ignored-until-execution",
			},
		},
		{name: "malformed user boolean", extras: map[string]any{"user-invocable": "yes"}, wantErr: "user-invocable"},
		{name: "malformed model boolean", extras: map[string]any{"disable-model-invocation": 1}, wantErr: "disable-model-invocation"},
		{name: "malformed hint", extras: map[string]any{"argument-hint": true}, wantErr: "argument-hint"},
		{name: "invalid context", extras: map[string]any{"context": "full"}, wantErr: "context"},
		{name: "malformed agent", extras: map[string]any{"agent": []string{"reviewer"}}, wantErr: "agent"},
		{name: "malformed model", extras: map[string]any{"model": false}, wantErr: "model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skill := &Skill{Name: "review", Extras: tt.extras}
			got, err := InvocationFor(skill)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("InvocationFor() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("InvocationFor() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("InvocationFor() = %#v, want %#v", got, tt.want)
			}
			if tt.extras != nil {
				if _, ok := skill.Extras["unknown"]; tt.name == "all extensions" && !ok {
					t.Fatal("InvocationFor removed unrelated extras")
				}
			}
		})
	}
}

func TestExpandInvocationArguments(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		raw     string
		want    string
		wantErr string
	}{
		{
			name: "raw and indexed placeholders",
			body: "Review $ARGUMENTS[0] with $ARGUMENTS[1]. Raw: $ARGUMENTS",
			raw:  `internal/config "lifecycle focus"`,
			want: `Review internal/config with lifecycle focus. Raw: internal/config "lifecycle focus"`,
		},
		{
			name: "single and double quotes plus escapes",
			body: "$ARGUMENTS[0]|$ARGUMENTS[1]|$ARGUMENTS[2]|$ARGUMENTS",
			raw:  `'first argument' "second argument" third\ value`,
			want: `first argument|second argument|third value|'first argument' "second argument" third\ value`,
		},
		{
			name: "missing position",
			body: "before<$ARGUMENTS[9]>after",
			raw:  "one",
			want: "before<>after",
		},
		{
			name: "empty arguments",
			body: "raw=[$ARGUMENTS], first=[$ARGUMENTS[0]]",
			want: "raw=[], first=[]",
		},
		{
			name: "one pass only",
			body: "$ARGUMENTS[0] -- $ARGUMENTS",
			raw:  `'$ARGUMENTS[1]' value`,
			want: `$ARGUMENTS[1] -- '$ARGUMENTS[1]' value`,
		},
		{
			name: "code blocks",
			body: "```text\n$ARGUMENTS[0]\n```",
			raw:  "inside",
			want: "```text\ninside\n```",
		},
		{
			name: "arguments appended when no placeholder",
			body: "Review the working tree.",
			raw:  "internal/config lifecycle",
			want: "Review the working tree.\n\n---\n\n## Invocation arguments\n\ninternal/config lifecycle",
		},
		{
			name: "body unchanged without arguments or placeholders",
			body: "Review the working tree.",
			want: "Review the working tree.",
		},
		{
			name:    "unterminated single quote",
			body:    "$ARGUMENTS[0]",
			raw:     "'broken",
			wantErr: "unterminated",
		},
		{
			name:    "unterminated double quote",
			body:    "$ARGUMENTS[0]",
			raw:     `"broken`,
			wantErr: "unterminated",
		},
		{
			name: "no expansion",
			body: "$ARGUMENTS[0]|$ARGUMENTS[1]|$ARGUMENTS[2]",
			raw:  `$HOME '*.go' '$(echo nope)'`,
			want: `$HOME|*.go|$(echo nope)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandInvocationArguments(tt.body, tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ExpandInvocationArguments() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExpandInvocationArguments() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ExpandInvocationArguments() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseInvocationArguments(t *testing.T) {
	got, err := ParseInvocationArguments(`one "two three" '' four\ five`)
	if err != nil {
		t.Fatalf("ParseInvocationArguments() error = %v", err)
	}
	want := []string{"one", "two three", "", "four five"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseInvocationArguments() = %#v, want %#v", got, want)
	}
}

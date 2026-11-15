package contain

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeOAuthFixture(t *testing.T, dir, filename, payload string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWorkspaceCredentialOverrideHydratesEnv(t *testing.T) {
	for _, override := range []string{"custom", "yaml", "home", "missing", "missing-file", "relative", "file"} {
		t.Run(override, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			xdg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdg)
			writeOAuthFixture(t, filepath.Join(xdg, "term-llm"), "chatgpt_oauth.json", "default-must-not-be-used")
			custom := filepath.Join(home, "oauth")
			value := custom
			want := "custom-secret"
			switch override {
			case "home":
				value = "~/oauth"
			case "missing", "missing-file":
				want = ""
				if override == "missing-file" {
					if err := os.MkdirAll(custom, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "relative":
				value = "relative-oauth"
			case "file":
				value = filepath.Join(home, "not-a-directory")
				if err := os.WriteFile(value, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if override == "custom" || override == "home" || override == "yaml" {
				writeOAuthFixture(t, custom, "chatgpt_oauth.json", want)
			}
			setContainCredentialSource(t, xdg, value, override == "yaml")
			dir, err := CreateWorkspace("oauthbox", CreateOptions{Template: "agent", CWD: t.TempDir(), NoInput: true, Values: map[string]string{"provider": "chatgpt"}})
			if override == "relative" || override == "file" {
				if err == nil || !strings.Contains(err.Error(), "TERM_LLM_CREDENTIALS_DIR") {
					t.Fatalf("CreateWorkspace error = %v, want invalid override", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, ".env"))
			if err != nil {
				t.Fatal(err)
			}
			wantLine := "TERM_LLM_CHATGPT_OAUTH_JSON_B64=" + base64.StdEncoding.EncodeToString([]byte(want))
			if !strings.Contains(string(data), wantLine+"\n") || strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte("default-must-not-be-used"))) {
				t.Fatal(".env did not use only the authoritative credential directory")
			}
		})
	}
}

func TestExecBuiltinSeedCredentialOverride(t *testing.T) {
	for _, provider := range []string{"chatgpt", "copilot"} {
		for _, override := range []string{"custom", "yaml", "missing", "missing-file", "relative", "file"} {
			t.Run(provider+"/"+override, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				xdg := t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", xdg)
				clearConsoleEnvForContainTest(t)
				filename := provider + "_oauth.json"
				writeOAuthFixture(t, filepath.Join(xdg, "term-llm"), filename, "default-must-not-be-used")
				t.Setenv("TERM_LLM_CREDENTIALS_DIR", t.TempDir())
				dir, err := CreateWorkspace("seedbox", CreateOptions{Template: "agent", CWD: t.TempDir(), NoInput: true, Values: map[string]string{"provider": "chatgpt"}})
				if err != nil {
					t.Fatal(err)
				}
				info, err := ReadComposeInfo(filepath.Join(dir, "compose.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				recipe := info.Hints.ExecRecipes["seed-"+provider+"-auth"]
				if len(recipe.CopyFiles) != 1 || recipe.CopyFiles[0].From != "[[host_credentials_dir]]/"+filename {
					t.Fatalf("generated recipe lost runtime credential placeholder: %+v", recipe)
				}
				// Change the override AFTER rendering: Exec must use the new path.
				custom := filepath.Join(t.TempDir(), "oauth")
				value := custom
				switch override {
				case "custom", "yaml":
					writeOAuthFixture(t, custom, filename, "exec-time-secret")
				case "missing-file":
					if err := os.MkdirAll(custom, 0o700); err != nil {
						t.Fatal(err)
					}
				case "relative":
					value = "relative-oauth"
				case "file":
					value = filepath.Join(t.TempDir(), "file")
					if err := os.WriteFile(value, []byte("fixture"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				setContainCredentialSource(t, xdg, value, override == "yaml")
				runner := &fakeRunner{}
				err = Exec(context.Background(), runner, "seedbox", []string{"seed-" + provider + "-auth"}, nil, io.Discard, io.Discard)
				if override != "custom" && override != "yaml" {
					if err == nil || len(runner.runs) != 0 {
						t.Fatalf("Exec = %v, runs = %v; want failure before any container copy", err, runner.runs)
					}
					if strings.HasPrefix(override, "missing") && !strings.Contains(err.Error(), filepath.Join(custom, filename)) {
						t.Fatalf("missing-source error does not identify override: %v", err)
					}
					if !strings.HasPrefix(override, "missing") && !strings.Contains(err.Error(), "TERM_LLM_CREDENTIALS_DIR") {
						t.Fatalf("resolver error not propagated: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(runner.stdins) != 2 || string(runner.stdins[0]) != "exec-time-secret" {
					t.Fatalf("copy did not read exec-time override: %q", runner.stdins)
				}
				args := strings.Join(runner.runs[0], " ")
				if !strings.Contains(args, recipe.CopyFiles[0].To) || strings.Contains(args, "TERM_LLM_CREDENTIALS_DIR=") {
					t.Fatalf("container destination/environment changed: %s", args)
				}
			})
		}
	}
}

func TestRecipeContextOnlyResolvesCredentialsWhenUsed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "relative-invalid")
	if _, err := newRecipeTemplateContext("box", t.TempDir(), "/home/agent", "{{config_dir}}/unrelated-file"); err != nil {
		t.Fatalf("unrelated recipe depends on OAuth directory: %v", err)
	}
	if _, err := newRecipeTemplateContext("box", t.TempDir(), "/home/agent", "[[host_credentials_dir]]/chatgpt_oauth.json"); err == nil {
		t.Fatal("credential recipe ignored invalid override")
	}
}

func setContainCredentialSource(t *testing.T, xdg, value string, fromYAML bool) {
	t.Helper()
	if !fromYAML {
		t.Setenv("TERM_LLM_CREDENTIALS_DIR", value)
		return
	}
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	path := filepath.Join(xdg, "term-llm", "config.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("credentials_dir: %q\n", value)), 0o600); err != nil {
		t.Fatal(err)
	}
}

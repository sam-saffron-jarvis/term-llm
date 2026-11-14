package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/fileprivacy"
)

func TestCredentialDir(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	xdg := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, override, xdg, want string
		invalid                   bool
	}{
		{name: "home default", want: filepath.Join(home, ".config", "term-llm")},
		{name: "XDG default", xdg: xdg, want: filepath.Join(xdg, "term-llm")},
		{name: "custom missing directory", override: filepath.Join(home, "oauth"), xdg: xdg, want: filepath.Join(home, "oauth")},
		{name: "home expansion", override: "~/oauth", want: filepath.Join(home, "oauth")},
		{name: "bare home", override: "~", want: home},
		{name: "relative", override: "oauth", invalid: true},
		{name: "named home unsupported", override: "~someone/oauth", invalid: true},
		{name: "no env expansion", override: "${HOME}/oauth", invalid: true},
		{name: "no secret resolver", override: "file:///oauth", invalid: true},
		{name: "whitespace is not unset", override: " ", invalid: true},
		{name: "file not directory", override: file, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", tc.override)
			got, err := Dir()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "TERM_LLM_CREDENTIALS_DIR") {
					t.Fatalf("Dir() = %q, %v; want override error", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Dir() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

type credentialStoreTest struct {
	name   string
	path   func() (string, error)
	save   func() error
	load   func() error
	clear  func() error
	exists func() bool
}

func nativeCredentialStores() []credentialStoreTest {
	return []credentialStoreTest{
		{"chatgpt", ChatGPTCredentialsPath, func() error {
			return SaveChatGPTCredentials(&ChatGPTCredentials{AccessToken: "fixture", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()})
		}, func() error { _, err := GetChatGPTCredentials(); return err }, ClearChatGPTCredentials, ChatGPTCredentialsExist},
		{"grok", GrokCredentialsPath, func() error {
			return SaveGrokCredentials(&GrokCredentials{AccessToken: "fixture", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), AccountID: "acct_1"})
		}, func() error { _, err := GetGrokCredentials(); return err }, ClearGrokCredentials, GrokCredentialsExist},
		{"copilot", CopilotCredentialsPath, func() error {
			return SaveCopilotCredentials(&CopilotCredentials{AccessToken: "fixture"})
		}, func() error { _, err := GetCopilotCredentials(); return err }, ClearCopilotCredentials, CopilotCredentialsExist},
	}
}

func TestNativeCredentialStoresOverride(t *testing.T) {
	for _, store := range nativeCredentialStores() {
		t.Run(store.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			defaultPath, err := store.path()
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(defaultPath)
			if err != nil {
				t.Fatal(err)
			}
			custom := filepath.Join(t.TempDir(), "oauth")
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", custom)
			path, err := store.path()
			if err != nil || path != filepath.Join(custom, store.name+"_oauth.json") {
				t.Fatalf("path = %q, %v", path, err)
			}
			if store.exists() || store.load() == nil {
				t.Fatal("missing override fell back to default credentials")
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			if !store.exists() || store.load() != nil {
				t.Fatal("custom credentials not readable")
			}
			for _, p := range []string{custom, path} {
				info, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if info.IsDir() {
					err = fileprivacy.CheckDir(info)
				} else {
					err = fileprivacy.CheckFile(info)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("invalid-json"), 0o600); err != nil {
				t.Fatal(err)
			}
			if store.load() == nil {
				t.Fatal("corrupt override fell back to default credentials")
			}
			if err := store.clear(); err != nil {
				t.Fatal(err)
			}
			if store.exists() || store.load() == nil {
				t.Fatal("clear did not remove custom credentials")
			}
			// Invalid overrides must also fail closed for every operation.
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", "relative")
			if store.exists() || store.load() == nil || store.save() == nil || store.clear() == nil {
				t.Fatal("invalid override did not fail closed")
			}
			unchanged, err := os.ReadFile(defaultPath)
			if err != nil || string(unchanged) != string(original) {
				t.Fatalf("default credentials changed: %v", err)
			}
		})
	}
}

// Exercise the existing rotation, single-use refresh, CAS and process-lock
// regressions against a custom directory as well as their default-path runs.
func TestCredentialOverrideRefreshAndLocks(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"chatgpt rotation", TestRefreshChatGPTCredentialsAdoptsRotatedCredentials},
		{"chatgpt concurrent login", TestRefreshChatGPTCredentialsDoesNotHoldLockDuringNetwork},
		{"chatgpt disk generation", TestRefreshChatGPTCredentialsUsesDiskGenerationAsCASBaseline},
		{"chatgpt conditional clear", TestClearChatGPTCredentialsIfRefreshTokenPreservesNewerCredentials},
		{"chatgpt single-use refresh", TestConcurrentChatGPTRefreshCoalescesExchanges},
		{"chatgpt process mutation lock", TestChatGPTCredentialsLockSerializesProcesses},
		{"chatgpt process refresh lock", TestChatGPTRefreshCoalescesProcesses},
		{"grok rotation", TestGrokCredentialRefreshRotationContinuityAndConcurrency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "oauth")
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", dir)
			tc.run(t)
			filename := "chatgpt_oauth.json"
			if strings.HasPrefix(tc.name, "grok") {
				filename = grokCredentialsFile
			}
			for _, suffix := range []string{".lock", ".refresh.lock"} {
				if suffix == ".refresh.lock" && (filename == grokCredentialsFile || strings.Contains(tc.name, "conditional clear") || strings.Contains(tc.name, "process mutation")) {
					continue // These operations use only the mutation lock.
				}
				info, err := os.Stat(filepath.Join(dir, filename+suffix))
				if err != nil {
					t.Fatal(err)
				}
				if err := fileprivacy.CheckFile(info); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

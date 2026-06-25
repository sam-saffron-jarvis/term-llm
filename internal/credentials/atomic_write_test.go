package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveChatGPTCredentials_CreateTempFailureLeavesExistingFileUntouched(t *testing.T) {
	testCredentialSaveCreateTempFailureLeavesExistingFileUntouched(
		t,
		func() (string, error) {
			return getChatGPTCredentialsPath()
		},
		func() error {
			return SaveChatGPTCredentials(&ChatGPTCredentials{
				AccessToken:  "original-access",
				RefreshToken: "original-refresh",
				ExpiresAt:    123,
				AccountID:    "original-account",
			})
		},
		func() error {
			return SaveChatGPTCredentials(&ChatGPTCredentials{
				AccessToken:  "updated-access",
				RefreshToken: "updated-refresh",
				ExpiresAt:    456,
				AccountID:    "updated-account",
			})
		},
	)
}

func TestSaveCopilotCredentials_CreateTempFailureLeavesExistingFileUntouched(t *testing.T) {
	testCredentialSaveCreateTempFailureLeavesExistingFileUntouched(
		t,
		func() (string, error) {
			return getCopilotCredentialsPath()
		},
		func() error {
			return SaveCopilotCredentials(&CopilotCredentials{
				AccessToken: "original-access",
				ExpiresAt:   123,
			})
		},
		func() error {
			return SaveCopilotCredentials(&CopilotCredentials{
				AccessToken: "updated-access",
				ExpiresAt:   456,
			})
		},
	)
}

func testCredentialSaveCreateTempFailureLeavesExistingFileUntouched(t *testing.T, pathFn func() (string, error), saveOriginal func() error, saveUpdated func() error) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("directory permissions behave differently on Windows")
	}

	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	if err := saveOriginal(); err != nil {
		t.Fatalf("save original credentials: %v", err)
	}

	path, err := pathFn()
	if err != nil {
		t.Fatalf("get credentials path: %v", err)
	}

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original credentials: %v", err)
	}

	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat credentials directory: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod credentials directory read-only: %v", err)
	}
	defer func() {
		if err := os.Chmod(dir, info.Mode().Perm()); err != nil {
			t.Fatalf("restore credentials directory permissions: %v", err)
		}
	}()

	err = saveUpdated()
	if err == nil {
		t.Fatal("expected save to fail")
	}
	if !strings.Contains(err.Error(), "create temp file") {
		t.Fatalf("error = %v, want create temp file", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials after failed save: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("credentials changed on failed save: got %q want %q", got, original)
	}
}

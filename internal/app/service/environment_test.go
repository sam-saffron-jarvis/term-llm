package service

import (
	"fmt"
	"github.com/term-llm/term-llm/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/userservice"
)

func TestServiceInstallCredentialsDir(t *testing.T) {
	home := isolatedServiceHome(t)
	// The runner's HOME, not the invoking process's, owns ~ expansion.
	serviceHome := t.TempDir()
	e := serviceEnvironment{home: serviceHome}
	file := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing", "oauth")
	for _, tc := range []struct {
		name, value, want string
		invalid           bool
	}{
		{name: "empty"},
		{name: "absolute existing", value: home, want: home},
		{name: "absolute missing", value: missing, want: missing},
		{name: "home", value: "~", want: serviceHome},
		{name: "home child", value: "~/oauth", want: filepath.Join(serviceHome, "oauth")},
		{name: "clean", value: home + "/child/../oauth", want: filepath.Join(home, "oauth")},
		{name: "literal environment", value: filepath.Join(home, "$OTHER"), want: filepath.Join(home, "$OTHER")},
		{name: "relative", value: "oauth", invalid: true},
		{name: "named user", value: "~someone/oauth", invalid: true},
		{name: "environment", value: "$HOME/oauth", invalid: true},
		{name: "secret reference", value: "op://vault/path", invalid: true},
		{name: "file", value: file, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", tc.value)
			spec, err := testApp().buildServiceInstallSpec(e, "web", nil, InstallOptions{}, nil)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "TERM_LLM_CREDENTIALS_DIR") {
					t.Fatalf("expected override error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := spec.Environment["TERM_LLM_CREDENTIALS_DIR"]; got != tc.want {
				t.Fatalf("directory = %q, want %q", got, tc.want)
			}
			if spec.Environment["HOME"] != serviceHome {
				t.Fatal("wrong runner HOME")
			}
		})
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("install created missing credentials directory: %v", err)
	}
}

func TestServiceReinstallCredentialsDir(t *testing.T) {
	home := isolatedServiceHome(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := serviceEnvironment{home: home}
	const key = "TERM_LLM_CREDENTIALS_DIR"
	for _, recorded := range []string{"", filepath.Join(home, "recorded")} {
		for _, ambient := range []string{"", "~/fresh", "relative-invalid"} {
			for _, refresh := range []bool{false, true} {
				t.Setenv(key, ambient)
				old := &userservice.Spec{Kind: "web", Binary: self, Directory: home, Environment: map[string]string{"HOME": home}}
				if recorded != "" {
					old.Environment[key] = recorded
				}
				spec, err := testApp().buildServiceInstallSpec(e, "web", nil, InstallOptions{RefreshEnv: refresh}, old)
				if refresh && ambient == "relative-invalid" {
					if err == nil {
						t.Fatal("refresh accepted invalid override")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				want := recorded
				if refresh {
					want = ""
					if ambient != "" {
						want = filepath.Join(home, "fresh")
					}
				}
				if got := spec.Environment[key]; got != want {
					t.Fatalf("recorded=%q ambient=%q refresh=%v: got %q, want %q", recorded, ambient, refresh, got, want)
				}
				if old.Environment[key] != recorded {
					t.Fatal("mutated recorded environment")
				}
			}
		}
	}
}

func TestServiceKeepsYAMLCredentialsDirOutOfEnvironment(t *testing.T) {
	home := isolatedServiceHome(t)
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	dir, err := config.GetConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	first, second := t.TempDir(), t.TempDir()
	for _, selected := range []string{first, second} {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("credentials_dir: %q\n", selected)), 0o600); err != nil {
			t.Fatal(err)
		}
		spec, err := testApp().buildServiceInstallSpec(serviceEnvironment{home: home}, "web", nil, InstallOptions{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Environment["TERM_LLM_CREDENTIALS_DIR"] != "" {
			t.Fatal("YAML choice was frozen as an overriding service environment value")
		}
		if got, err := config.NativeCredentialsDir(); err != nil || got != selected {
			t.Fatalf("service config selection = %q, %v", got, err)
		}
	}
}

package contain

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samsaffron/term-llm/internal/config"
)

const defaultWorkspacePath = "/workspace"

var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)
var unresolvedPlaceholderPattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)

type CreateOptions struct {
	Template string
	CWD      string
	Values   map[string]string
	NoInput  bool
	Stdin    io.Reader
	Stdout   io.Writer
}

type PlaceholderValues map[string]string

// NewPlaceholderValues builds the supported replacement values for contain
// templates.
func NewPlaceholderValues(name, cwd, configDir, containersDir, composePath string) PlaceholderValues {
	home, _ := os.UserHomeDir()
	agentImageDir := ""
	agentImageDockerfile := ""
	if dir, err := ImageDir("agent"); err == nil {
		agentImageDir = dir
		agentImageDockerfile = filepath.Join(dir, "Dockerfile")
	}
	agentImageHash, _ := AgentImageHash()
	return PlaceholderValues{
		"name":                   name,
		"project_name":           ProjectName(name),
		"config_dir":             configDir,
		"compose_path":           composePath,
		"containers_dir":         containersDir,
		"cwd":                    cwd,
		"home":                   home,
		"workspace":              defaultWorkspacePath,
		"agent_image_dir":        agentImageDir,
		"agent_image_dockerfile": agentImageDockerfile,
		"agent_image_hash":       agentImageHash,
		"AGENT_NAME":             name,
	}
}

// RenderTemplateContent applies simple placeholder substitution to data.
func RenderTemplateContent(data []byte, values PlaceholderValues) ([]byte, error) {
	var unknown []string
	seenUnknown := map[string]bool{}
	out := placeholderPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		sub := placeholderPattern.FindSubmatch(match)
		key := strings.TrimSpace(string(sub[1]))
		value, ok := values[key]
		if !ok {
			if !seenUnknown[key] {
				unknown = append(unknown, key)
				seenUnknown[key] = true
			}
			return match
		}
		return []byte(value)
	})
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown template placeholder(s): %s", strings.Join(unknown, ", "))
	}
	if unresolved := unresolvedPlaceholderPattern.Find(out); unresolved != nil {
		return nil, fmt.Errorf("unknown template placeholder %q", string(unresolved))
	}
	return out, nil
}

// CreateWorkspace renders the selected template into the named global contain
// workspace directory. Existing targets are never overwritten.
func CreateWorkspace(name string, opts CreateOptions) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	cwd := opts.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	containersDir, err := ContainersRoot()
	if err != nil {
		return "", err
	}
	targetDir, err := ContainerDir(name)
	if err != nil {
		return "", err
	}
	composePath, err := ComposePath(name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(targetDir); err == nil {
		return "", fmt.Errorf("contain workspace %q already exists at %s", name, targetDir)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmpl, err := LoadTemplate(opts.Template)
	if err != nil {
		return "", err
	}
	if tmpl.Builtin && tmpl.Name == "agent" {
		if _, err := SyncImage("agent", true); err != nil {
			return "", fmt.Errorf("sync agent image: %w", err)
		}
	}
	promptValues, err := ResolveTemplateValues(tmpl.Descriptor, opts.Values, opts.NoInput, opts.Stdin, opts.Stdout)
	if err != nil {
		return "", err
	}
	if tmpl.Builtin && tmpl.Name == "agent" {
		if err := addAgentTemplateValues(promptValues); err != nil {
			return "", err
		}
	}
	values := NewPlaceholderValues(name, cwd, targetDir, containersDir, composePath)
	for k, v := range promptValues {
		values[k] = v
	}

	type renderedFile struct {
		Path string
		Data []byte
		Mode os.FileMode
	}
	rendered := make([]renderedFile, 0, len(tmpl.Files))
	for _, f := range tmpl.Files {
		targetRel, err := safeTemplateTargetPath(f.Path)
		if err != nil {
			return "", err
		}
		data, err := RenderTemplateContent(f.Data, values)
		if err != nil {
			return "", fmt.Errorf("render %s: %w", f.Path, err)
		}
		mode := f.Mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		if filepath.Base(targetRel) == ".env" {
			mode = 0o600
		}
		if mode&0o111 != 0 {
			mode |= 0o111
		}
		rendered = append(rendered, renderedFile{Path: targetRel, Data: data, Mode: mode})
	}

	if err := os.MkdirAll(containersDir, 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("contain workspace %q already exists at %s", name, targetDir)
		}
		return "", err
	}

	for _, f := range rendered {
		path := filepath.Join(targetDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := writeNewFile(path, f.Data, f.Mode); err != nil {
			return "", err
		}
	}
	return targetDir, nil
}

func addAgentTemplateValues(values map[string]string) error {
	if values == nil {
		return nil
	}
	if _, ok := values["chatgpt_oauth_json_b64"]; ok {
		return nil
	}
	values["chatgpt_oauth_json_b64"] = ""
	if values["provider"] != "chatgpt" {
		return nil
	}
	configDir, err := config.GetConfigDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(configDir, "chatgpt_oauth.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	values["chatgpt_oauth_json_b64"] = base64.StdEncoding.EncodeToString(data)
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := bytes.NewReader(data).WriteTo(file)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func safeTemplateTargetPath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("template file path must not be empty")
	}
	fromSlash := filepath.FromSlash(rel)
	if filepath.IsAbs(fromSlash) {
		return "", fmt.Errorf("template file path %q must be relative", rel)
	}
	clean := filepath.Clean(fromSlash)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template file path %q escapes target directory", rel)
	}
	return clean, nil
}

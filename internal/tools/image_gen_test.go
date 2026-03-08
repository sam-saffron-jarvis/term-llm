package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestImageGenAutoApprove_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require special privileges on Windows")
	}

	// Set up directory structure:
	//   outputDir/           - the configured image output directory
	//   outputDir/real.png   - a real image file inside outputDir
	//   outsideDir/          - a directory outside the output directory
	//   outsideDir/secret.png - a sensitive file outside the output directory
	//   outputDir/link.png   - a symlink inside outputDir pointing to secret.png

	tmpDir := t.TempDir()
	// Resolve symlinks on tmpDir itself (macOS /var -> /private/var)
	tmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("failed to resolve tmpDir symlinks: %v", err)
	}

	outputDir := filepath.Join(tmpDir, "outputDir")
	outsideDir := filepath.Join(tmpDir, "outsideDir")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir outputDir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("mkdir outsideDir: %v", err)
	}

	// Create a real image in the output dir
	realImage := filepath.Join(outputDir, "real.png")
	if err := os.WriteFile(realImage, []byte("fake png data"), 0644); err != nil {
		t.Fatalf("write real.png: %v", err)
	}

	// Create a sensitive file outside the output dir
	secretFile := filepath.Join(outsideDir, "secret.png")
	if err := os.WriteFile(secretFile, []byte("sensitive data"), 0644); err != nil {
		t.Fatalf("write secret.png: %v", err)
	}

	// Create a symlink inside the output dir pointing to the secret file
	symlinkPath := filepath.Join(outputDir, "link.png")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Set up an approval manager that denies everything not auto-approved.
	// If the symlink escape works, the auto-approve logic will skip the
	// approval check and the test will NOT see a denial.
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	var approvalRequested []string
	mgr.PromptUIFunc = func(path string, isWrite bool, isShell bool) (ApprovalResult, error) {
		approvalRequested = append(approvalRequested, path)
		return ApprovalResult{Choice: ApprovalChoiceDeny}, nil
	}

	cfg := &config.Config{
		Image: config.ImageConfig{
			Provider:  "debug",
			OutputDir: outputDir,
		},
	}

	tool := NewImageGenerateTool(mgr, cfg, "debug", nil, "", "")

	// Test 1: A symlink inside outputDir pointing outside should NOT be auto-approved.
	// The approval manager should be consulted and deny access.
	args, _ := json.Marshal(ImageGenerateArgs{
		Prompt:     "test prompt",
		InputImage: symlinkPath,
	})

	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out.Content, "permission_denied") && !strings.Contains(out.Content, "access denied") {
		t.Errorf("expected permission denied for symlink escape, got: %s", out.Content)
	}
	if len(approvalRequested) == 0 {
		t.Error("expected approval to be requested for symlink pointing outside output dir, but auto-approve bypassed it")
	}

	// Test 2: A real file inside outputDir should be auto-approved (no prompt).
	approvalRequested = nil
	args2, _ := json.Marshal(ImageGenerateArgs{
		Prompt:     "test prompt",
		InputImage: realImage,
	})

	out2, err := tool.Execute(context.Background(), args2)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(approvalRequested) > 0 {
		t.Errorf("expected real file inside output dir to be auto-approved, but approval was requested for: %v", approvalRequested)
	}
	// The debug provider should succeed (it generates a random image)
	if strings.Contains(out2.Content, "permission_denied") || strings.Contains(out2.Content, "access denied") {
		t.Errorf("expected success for real file in output dir, got: %s", out2.Content)
	}
}

func TestImageGenerateTool_ServeModeStripsTerminalParams(t *testing.T) {
	cfg := &config.Config{}
	tool := NewImageGenerateTool(nil, cfg, "", nil, "", "")

	// Default: has copy_to_clipboard and show_image
	spec := tool.Spec()
	props := spec.Schema["properties"].(map[string]interface{})
	if _, ok := props["copy_to_clipboard"]; !ok {
		t.Error("expected copy_to_clipboard in default spec")
	}
	if _, ok := props["show_image"]; !ok {
		t.Error("expected show_image in default spec")
	}

	// Serve mode: stripped
	tool.serveMode = true
	spec = tool.Spec()
	props = spec.Schema["properties"].(map[string]interface{})
	if _, ok := props["copy_to_clipboard"]; ok {
		t.Error("copy_to_clipboard should be stripped in serve mode")
	}
	if _, ok := props["show_image"]; ok {
		t.Error("show_image should be stripped in serve mode")
	}
	// Core params still present
	if _, ok := props["prompt"]; !ok {
		t.Error("prompt should still be present in serve mode")
	}
	if _, ok := props["input_image"]; !ok {
		t.Error("input_image should still be present in serve mode")
	}
}

func TestShowImageTool_ServeModeStripsClipboard(t *testing.T) {
	tool := NewShowImageTool(nil)

	// Default: has copy_to_clipboard
	spec := tool.Spec()
	props := spec.Schema["properties"].(map[string]any)
	if _, ok := props["copy_to_clipboard"]; !ok {
		t.Error("expected copy_to_clipboard in default spec")
	}
	if !strings.Contains(spec.Description, "clipboard") {
		t.Error("expected clipboard mention in default description")
	}

	// Serve mode: stripped
	tool.serveMode = true
	spec = tool.Spec()
	props = spec.Schema["properties"].(map[string]any)
	if _, ok := props["copy_to_clipboard"]; ok {
		t.Error("copy_to_clipboard should be stripped in serve mode")
	}
	if strings.Contains(spec.Description, "clipboard") {
		t.Error("clipboard should not be mentioned in serve mode description")
	}
	// Core params still present
	if _, ok := props["file_path"]; !ok {
		t.Error("file_path should still be present in serve mode")
	}
}

func TestImageGenAutoApprove_EvalSymlinksFailure(t *testing.T) {
	// When EvalSymlinks fails (e.g., file doesn't exist), the code should
	// NOT auto-approve. The path falls through to CheckPathApproval which
	// will error because the file doesn't exist (FILE_NOT_FOUND from
	// canonicalizePath). The key point is that a non-existent path inside
	// the output dir is never silently auto-approved.
	tmpDir := t.TempDir()
	tmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("resolve tmpDir: %v", err)
	}

	outputDir := filepath.Join(tmpDir, "outputDir")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Reference a non-existent file inside the output dir
	nonexistent := filepath.Join(outputDir, "does-not-exist.png")

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	cfg := &config.Config{
		Image: config.ImageConfig{
			Provider:  "debug",
			OutputDir: outputDir,
		},
	}

	tool := NewImageGenerateTool(mgr, cfg, "debug", nil, "", "")

	args, _ := json.Marshal(ImageGenerateArgs{
		Prompt:     "test prompt",
		InputImage: nonexistent,
	})

	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// Should NOT succeed - the file doesn't exist and should not be auto-approved
	if !strings.Contains(out.Content, "FILE_NOT_FOUND") && !strings.Contains(out.Content, "permission_denied") {
		t.Errorf("expected error for non-existent file (not auto-approved), got: %s", out.Content)
	}
}

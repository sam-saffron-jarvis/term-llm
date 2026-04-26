package contain

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, name string, args []string, opts RunOptions) error
	Output(ctx context.Context, name string, args []string, opts RunOptions) ([]byte, error)
}

type RunOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Dir    string
}

var consoleEnvNames = []string{
	"TERM",
	"COLORTERM",
	"CLICOLOR",
	"CLICOLOR_FORCE",
	"FORCE_COLOR",
	"NO_COLOR",
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args []string, opts RunOptions) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	cmd.Dir = opts.Dir
	return cmd.Run()
}

func (OSRunner) Output(ctx context.Context, name string, args []string, opts RunOptions) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = opts.Stdin
	cmd.Stderr = opts.Stderr
	cmd.Dir = opts.Dir
	return cmd.Output()
}

func ComposeBaseArgs(name string) ([]string, string, error) {
	if err := ValidateName(name); err != nil {
		return nil, "", err
	}
	dir, err := ContainerDir(name)
	if err != nil {
		return nil, "", err
	}
	compose, err := ComposePath(name)
	if err != nil {
		return nil, "", err
	}
	return []string{"compose", "-f", compose, "--project-directory", dir, "-p", ProjectName(name)}, dir, nil
}

func Start(ctx context.Context, runner Runner, name string, stdout, stderr io.Writer) error {
	args, dir, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	if err := ensureComposeDefinition(name); err != nil {
		return err
	}
	if err := syncManagedImagesForWorkspace(name); err != nil {
		return err
	}
	args = append(args, "up", "-d", "--build")
	return runner.Run(ctx, "docker", args, RunOptions{Stdout: stdout, Stderr: stderr, Dir: dir})
}

func Stop(ctx context.Context, runner Runner, name string, stdout, stderr io.Writer) error {
	args, dir, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	if err := ensureComposeDefinition(name); err != nil {
		return err
	}
	args = append(args, "stop")
	return runner.Run(ctx, "docker", args, RunOptions{Stdout: stdout, Stderr: stderr, Dir: dir})
}

// Remove permanently deletes a contain workspace: Compose resources are brought
// down with volumes removed, then the workspace definition directory is deleted.
func Remove(ctx context.Context, runner Runner, name string, stdout, stderr io.Writer) error {
	args, dir, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	if err := ensureComposeDefinitionExists(name); err != nil {
		return err
	}
	args = append(args, "down", "--volumes", "--remove-orphans")
	if err := runner.Run(ctx, "docker", args, RunOptions{Stdout: stdout, Stderr: stderr, Dir: dir}); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove contain workspace directory %s: %w", dir, err)
	}
	return nil
}

// Rebuild rebuilds workspace images without cache, pulls newer base images where
// Compose can, and recreates containers while preserving volumes/networks.
func Rebuild(ctx context.Context, runner Runner, name string, stdout, stderr io.Writer) error {
	args, dir, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	if err := ensureComposeDefinition(name); err != nil {
		return err
	}
	if err := syncManagedImagesForWorkspace(name); err != nil {
		return err
	}
	buildArgs := append(append([]string{}, args...), "build", "--pull", "--no-cache")
	if err := runner.Run(ctx, "docker", buildArgs, RunOptions{Stdout: stdout, Stderr: stderr, Dir: dir}); err != nil {
		return err
	}
	upArgs := append(append([]string{}, args...), "up", "-d", "--force-recreate")
	return runner.Run(ctx, "docker", upArgs, RunOptions{Stdout: stdout, Stderr: stderr, Dir: dir})
}

func Exec(ctx context.Context, runner Runner, name string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(cmdArgs) == 0 {
		return Shell(ctx, runner, name, stdin, stdout, stderr)
	}
	info, dir, err := composeInfoForCommand(name)
	if err != nil {
		return err
	}
	args, _, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	args = append(args, "exec")
	args = appendConsoleEnvExecArgs(args)
	args = append(args, info.DefaultService())
	args = append(args, cmdArgs...)
	return runner.Run(ctx, "docker", args, RunOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr, Dir: dir})
}

func Shell(ctx context.Context, runner Runner, name string, stdin io.Reader, stdout, stderr io.Writer) error {
	info, dir, err := composeInfoForCommand(name)
	if err != nil {
		return err
	}
	args, _, err := ComposeBaseArgs(name)
	if err != nil {
		return err
	}
	args = append(args, "exec")
	args = appendConsoleEnvExecArgs(args)
	args = append(args, info.DefaultService(), info.Shell())
	return runner.Run(ctx, "docker", args, RunOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr, Dir: dir})
}

func appendConsoleEnvExecArgs(args []string) []string {
	for _, name := range consoleEnvNames {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		args = append(args, "-e", name+"="+value)
	}
	return args
}

func DockerPS(ctx context.Context, runner Runner, stderr io.Writer) ([]byte, error) {
	args := []string{"ps", "-a", "--filter", "label=org.term-llm.contain=true", "--format", "{{json .}}"}
	return runner.Output(ctx, "docker", args, RunOptions{Stderr: stderr})
}

func syncManagedImagesForWorkspace(name string) error {
	dir, err := ContainerDir(name)
	if err != nil {
		return err
	}
	compose, err := ComposePath(name)
	if err != nil {
		return err
	}
	info, err := ReadComposeInfo(compose)
	if err != nil || info.Invalid {
		return nil
	}
	agentDir, err := ImageDir("agent")
	if err != nil {
		return err
	}
	for _, service := range info.Services {
		if !sameBuildContext(service.BuildContext, agentDir, dir) {
			continue
		}
		if _, err := SyncImage("agent", true); err != nil {
			return fmt.Errorf("sync managed agent image: %w", err)
		}
		return nil
	}
	return nil
}

func sameBuildContext(contextPath, targetPath, composeDir string) bool {
	contextPath = strings.TrimSpace(contextPath)
	if contextPath == "" {
		return false
	}
	if !filepath.IsAbs(contextPath) {
		contextPath = filepath.Join(composeDir, contextPath)
	}
	contextAbs, err := filepath.Abs(contextPath)
	if err == nil {
		contextPath = contextAbs
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err == nil {
		targetPath = targetAbs
	}
	return filepath.Clean(contextPath) == filepath.Clean(targetPath)
}

func ensureComposeDefinition(name string) error {
	path, err := ComposePath(name)
	if err != nil {
		return err
	}
	if _, err := ReadComposeInfo(path); err != nil {
		return fmt.Errorf("contain workspace %q has no compose.yaml at %s: %w", name, path, err)
	}
	return nil
}

func ensureComposeDefinitionExists(name string) error {
	path, err := ComposePath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("contain workspace %q has no compose.yaml at %s: %w", name, path, err)
	}
	return nil
}

func composeInfoForCommand(name string) (ComposeInfo, string, error) {
	if err := ValidateName(name); err != nil {
		return ComposeInfo{}, "", err
	}
	dir, err := ContainerDir(name)
	if err != nil {
		return ComposeInfo{}, "", err
	}
	path, err := ComposePath(name)
	if err != nil {
		return ComposeInfo{}, "", err
	}
	info, err := ReadComposeInfo(path)
	if err != nil {
		return ComposeInfo{}, "", fmt.Errorf("contain workspace %q has no compose.yaml at %s: %w", name, path, err)
	}
	if info.Invalid {
		return ComposeInfo{}, "", fmt.Errorf("contain workspace %q has invalid compose.yaml: %s", name, info.InvalidReason)
	}
	return info, dir, nil
}

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ProjectApprovals stores per-project approval decisions.
// Approvals are persisted to ~/.config/term-llm/projects/<repo-hash>.yaml
type ProjectApprovals struct {
	RepoRoot      string    `yaml:"repo_root"`
	RepoName      string    `yaml:"repo_name"`
	UpdatedAt     time.Time `yaml:"updated_at"`
	ReadApproved  bool      `yaml:"read_approved"`  // Whole repo read access
	WriteApproved bool      `yaml:"write_approved"` // Whole repo write access
	ApprovedPaths []string  `yaml:"approved_paths"` // Individual approved paths (relative to repo)
	ShellPatterns []string  `yaml:"shell_patterns"` // Approved shell command patterns

	// Runtime fields (not persisted)
	filePath string     `yaml:"-"` // Path to the YAML file
	mu       sync.Mutex `yaml:"-"` // Protects concurrent access
}

// getProjectsDir returns the directory for storing project approvals.
func getProjectsDir() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "term-llm", "projects"), nil
}

// LoadProjectApprovals loads or creates approval data for a git repository.
// Returns nil if the repo root is empty or invalid.
func LoadProjectApprovals(repoRoot string) (*ProjectApprovals, error) {
	if repoRoot == "" {
		return nil, nil
	}

	repoID := GetGitRepoID(repoRoot)
	projectsDir, err := getProjectsDir()
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(projectsDir, repoID+".yaml")

	pa := &ProjectApprovals{
		RepoRoot:      repoRoot,
		RepoName:      filepath.Base(repoRoot),
		filePath:      filePath,
		ApprovedPaths: []string{},
		ShellPatterns: []string{},
	}

	// Try to load existing file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// No existing file, return empty approvals
			return pa, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, pa); err != nil {
		// Corrupted file, start fresh
		return &ProjectApprovals{
			RepoRoot:      repoRoot,
			RepoName:      filepath.Base(repoRoot),
			filePath:      filePath,
			ApprovedPaths: []string{},
			ShellPatterns: []string{},
		}, nil
	}

	// Ensure runtime fields are set
	pa.filePath = filePath
	if pa.ApprovedPaths == nil {
		pa.ApprovedPaths = []string{}
	}
	if pa.ShellPatterns == nil {
		pa.ShellPatterns = []string{}
	}

	return pa, nil
}

// Save persists the approval data to disk.
func (p *ProjectApprovals) Save() error {
	if p == nil || p.filePath == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.UpdatedAt = time.Now()

	// Ensure directory exists
	dir := filepath.Dir(p.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}

	return os.WriteFile(p.filePath, data, 0600)
}

// IsReadApproved checks if read access is approved for the entire repo.
func (p *ProjectApprovals) IsReadApproved() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ReadApproved
}

// IsWriteApproved checks if write access is approved for the entire repo.
func (p *ProjectApprovals) IsWriteApproved() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.WriteApproved
}

// ApproveRead approves read access for the entire repo.
func (p *ProjectApprovals) ApproveRead() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.ReadApproved = true
	p.mu.Unlock()
	return p.Save()
}

// ApproveWrite approves write access for the entire repo.
func (p *ProjectApprovals) ApproveWrite() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.WriteApproved = true
	p.mu.Unlock()
	return p.Save()
}

// IsPathApproved checks if a specific path is approved.
// Path should be absolute; it will be checked against repo root and approved paths.
func (p *ProjectApprovals) IsPathApproved(path string, isWrite bool) bool {
	if p == nil {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check whole-repo approval
	if isWrite {
		if p.WriteApproved {
			return true
		}
	} else {
		if p.ReadApproved {
			return true
		}
	}

	// Check if path is in approved paths list
	relPath := GetRelativePath(path, p.RepoRoot)
	for _, approved := range p.ApprovedPaths {
		// Check exact match or if path is under approved directory
		if relPath == approved || strings.HasPrefix(relPath, approved+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// ApprovePath adds a specific path to the approved list.
// Path should be absolute; it will be stored relative to the repo root.
func (p *ProjectApprovals) ApprovePath(path string) error {
	if p == nil {
		return nil
	}

	relPath := GetRelativePath(path, p.RepoRoot)

	p.mu.Lock()
	// Check if already approved
	for _, existing := range p.ApprovedPaths {
		if existing == relPath {
			p.mu.Unlock()
			return nil
		}
	}
	p.ApprovedPaths = append(p.ApprovedPaths, relPath)
	p.mu.Unlock()

	return p.Save()
}

// IsShellPatternApproved checks if a command matches any approved shell pattern.
func (p *ProjectApprovals) IsShellPatternApproved(command string) bool {
	if p == nil {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pattern := range p.ShellPatterns {
		if matchPattern(pattern, command) {
			return true
		}
	}

	return false
}

// ApproveShellPattern adds a shell command pattern to the approved list.
func (p *ProjectApprovals) ApproveShellPattern(pattern string) error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	// Check if already approved
	for _, existing := range p.ShellPatterns {
		if existing == pattern {
			p.mu.Unlock()
			return nil
		}
	}
	p.ShellPatterns = append(p.ShellPatterns, pattern)
	p.mu.Unlock()

	return p.Save()
}

// GenerateShellPattern creates a glob pattern from a command.
// For example: "go test ./..." -> "go test *"
func GenerateShellPattern(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return command
	}

	// Keep the command name, replace args with wildcard
	if len(parts) == 1 {
		return parts[0]
	}

	// For common commands, keep the first argument
	switch parts[0] {
	case "go", "npm", "yarn", "pnpm", "cargo", "make", "git":
		if len(parts) >= 2 {
			return parts[0] + " " + parts[1] + " *"
		}
	case "python", "python3", "node", "ruby", "perl":
		// Script execution - keep just the interpreter
		return parts[0] + " *"
	}

	// Default: command + wildcard
	return parts[0] + " *"
}

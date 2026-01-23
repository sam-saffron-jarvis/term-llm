package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	githubAPIBase       = "https://api.github.com"
	githubRawBase       = "https://raw.githubusercontent.com"
	githubClientTimeout = 30 * time.Second
	maxConcurrent       = 5 // Max parallel downloads
)

// GitHubClient handles GitHub API interactions for skill discovery and download.
type GitHubClient struct {
	httpClient *http.Client
	token      string // Optional GITHUB_TOKEN for higher rate limits
}

// GitHubRepoRef represents a parsed GitHub repository reference.
type GitHubRepoRef struct {
	Owner  string // Repository owner (user or org)
	Repo   string // Repository name
	Branch string // Branch name (default: "main", fallback: "master")
	Path   string // Path within repo to look for skills (default: "skills")
}

// GitHubContent represents a file or directory from the GitHub Contents API.
type GitHubContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" or "dir"
	Size        int    `json:"size"`
	DownloadURL string `json:"download_url"`
	URL         string `json:"url"` // API URL for this content
}

// DiscoveredSkill represents a skill found in a GitHub repository.
type DiscoveredSkill struct {
	Name        string        // Skill name (directory name)
	Path        string        // Full path in repo (e.g., "skills/remotion")
	Description string        // Description from SKILL.md if available
	FileCount   int           // Total file count including subdirectories
	RepoRef     GitHubRepoRef // Reference to the source repository
}

// NewGitHubClient creates a new GitHub client.
// It checks for GITHUB_TOKEN environment variable for authenticated requests.
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		httpClient: &http.Client{
			Timeout: githubClientTimeout,
		},
		token: os.Getenv("GITHUB_TOKEN"),
	}
}

// HasToken returns whether the client has an API token configured.
func (c *GitHubClient) HasToken() bool {
	return c.token != ""
}

// ParseRepoRef parses a GitHub repository reference string.
// Supported formats:
//   - owner/repo
//   - owner/repo@branch
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo/tree/branch
//   - https://github.com/owner/repo/tree/branch/path
func ParseRepoRef(ref string) (*GitHubRepoRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty repository reference")
	}

	result := &GitHubRepoRef{
		Branch: "main",
		Path:   "skills",
	}

	// Handle full URLs
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid URL: %w", err)
		}
		if u.Host != "github.com" {
			return nil, fmt.Errorf("only github.com URLs are supported")
		}

		// Parse path: /owner/repo or /owner/repo/tree/branch/path
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid GitHub URL: must include owner/repo")
		}
		result.Owner = parts[0]
		result.Repo = parts[1]

		// Handle /tree/branch/path format
		if len(parts) > 3 && parts[2] == "tree" {
			result.Branch = parts[3]
			if len(parts) > 4 {
				result.Path = strings.Join(parts[4:], "/")
			}
		}
		return result, nil
	}

	// Handle owner/repo[@branch] format
	// First check for @branch suffix
	if idx := strings.LastIndex(ref, "@"); idx != -1 {
		result.Branch = ref[idx+1:]
		ref = ref[:idx]
	}

	// Parse owner/repo
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid format: use owner/repo or owner/repo@branch")
	}
	result.Owner = parts[0]
	result.Repo = parts[1]

	if result.Owner == "" || result.Repo == "" {
		return nil, fmt.Errorf("invalid format: owner and repo cannot be empty")
	}

	return result, nil
}

// DiscoverSkills lists skill directories in a repository's skills folder.
func (c *GitHubClient) DiscoverSkills(ctx context.Context, ref GitHubRepoRef) ([]DiscoveredSkill, error) {
	// Try the specified branch first
	skills, err := c.discoverSkillsWithBranch(ctx, ref, ref.Branch)
	if err == nil {
		return skills, nil
	}

	// If main branch failed with 404, try master
	if ref.Branch == "main" && strings.Contains(err.Error(), "404") {
		ref.Branch = "master"
		skills, err = c.discoverSkillsWithBranch(ctx, ref, "master")
		if err == nil {
			return skills, nil
		}
	}

	return nil, err
}

func (c *GitHubClient) discoverSkillsWithBranch(ctx context.Context, ref GitHubRepoRef, branch string) ([]DiscoveredSkill, error) {
	// Fetch contents of the skills directory
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		githubAPIBase, ref.Owner, ref.Repo, ref.Path, branch)

	contents, err := c.fetchContents(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var skills []DiscoveredSkill
	for _, item := range contents {
		if item.Type != "dir" {
			continue
		}

		// Check if this directory contains a SKILL.md
		skillPath := item.Path
		skillMDURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s/SKILL.md?ref=%s",
			githubAPIBase, ref.Owner, ref.Repo, skillPath, branch)

		req, err := http.NewRequestWithContext(ctx, "HEAD", skillMDURL, nil)
		if err != nil {
			continue
		}
		c.addHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue // Not a valid skill directory
		}

		// Count files in this skill directory
		fileCount, _ := c.countFiles(ctx, ref, skillPath, branch)

		skill := DiscoveredSkill{
			Name:      item.Name,
			Path:      skillPath,
			FileCount: fileCount,
			RepoRef:   ref,
		}
		skill.RepoRef.Branch = branch

		skills = append(skills, skill)
	}

	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills found in %s/%s/%s", ref.Owner, ref.Repo, ref.Path)
	}

	return skills, nil
}

func (c *GitHubClient) countFiles(ctx context.Context, ref GitHubRepoRef, path, branch string) (int, error) {
	// Use the Git Trees API for efficient recursive listing
	// First get the tree SHA for this path
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		githubAPIBase, ref.Owner, ref.Repo, path, branch)

	contents, err := c.fetchContents(ctx, apiURL)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, item := range contents {
		if item.Type == "file" {
			count++
		} else if item.Type == "dir" {
			subCount, _ := c.countFiles(ctx, ref, item.Path, branch)
			count += subCount
		}
	}
	return count, nil
}

func (c *GitHubClient) fetchContents(ctx context.Context, apiURL string) ([]GitHubContent, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	c.addHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		// Check for rate limiting
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub API rate limit exceeded. Set GITHUB_TOKEN environment variable for higher limits")
		}
		return nil, fmt.Errorf("GitHub API forbidden (403): check repository access")
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found (404): repository or path does not exist")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var contents []GitHubContent
	if err := json.NewDecoder(resp.Body).Decode(&contents); err != nil {
		return nil, fmt.Errorf("parse GitHub response: %w", err)
	}

	return contents, nil
}

func (c *GitHubClient) addHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "term-llm/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// DownloadSkillDir downloads an entire skill directory to the destination.
func (c *GitHubClient) DownloadSkillDir(ctx context.Context, skill DiscoveredSkill, destDir string) error {
	// Create the destination directory
	skillDir := filepath.Join(destDir, skill.Name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill directory: %w", err)
	}

	// Get all files recursively
	files, err := c.listFilesRecursive(ctx, skill.RepoRef, skill.Path)
	if err != nil {
		return fmt.Errorf("list skill files: %w", err)
	}

	// Download files in parallel with limited concurrency
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrent)
	errChan := make(chan error, len(files))

	for _, file := range files {
		wg.Add(1)
		go func(f GitHubContent) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Calculate relative path within skill
			relPath := strings.TrimPrefix(f.Path, skill.Path+"/")
			localPath := filepath.Join(skillDir, relPath)

			// Create parent directories
			if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
				errChan <- fmt.Errorf("create directory for %s: %w", relPath, err)
				return
			}

			// Download file
			if err := c.downloadFile(ctx, f.DownloadURL, localPath); err != nil {
				errChan <- fmt.Errorf("download %s: %w", relPath, err)
				return
			}
		}(file)
	}

	wg.Wait()
	close(errChan)

	// Collect errors
	var errors []string
	for err := range errChan {
		errors = append(errors, err.Error())
	}
	if len(errors) > 0 {
		return fmt.Errorf("download errors:\n  %s", strings.Join(errors, "\n  "))
	}

	return nil
}

func (c *GitHubClient) listFilesRecursive(ctx context.Context, ref GitHubRepoRef, path string) ([]GitHubContent, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		githubAPIBase, ref.Owner, ref.Repo, path, ref.Branch)

	contents, err := c.fetchContents(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var files []GitHubContent
	for _, item := range contents {
		if item.Type == "file" {
			files = append(files, item)
		} else if item.Type == "dir" {
			subFiles, err := c.listFilesRecursive(ctx, ref, item.Path)
			if err != nil {
				return nil, err
			}
			files = append(files, subFiles...)
		}
	}
	return files, nil
}

func (c *GitHubClient) downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "term-llm/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// FetchSkillMD downloads and returns the SKILL.md content for a discovered skill.
func (c *GitHubClient) FetchSkillMD(ctx context.Context, skill DiscoveredSkill) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/%s/%s/%s/SKILL.md",
		githubRawBase, skill.RepoRef.Owner, skill.RepoRef.Repo, skill.RepoRef.Branch, skill.Path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "term-llm/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch SKILL.md returned %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// RawURL returns the raw GitHub URL for a skill's SKILL.md file.
func (skill *DiscoveredSkill) RawURL() string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/SKILL.md",
		githubRawBase, skill.RepoRef.Owner, skill.RepoRef.Repo, skill.RepoRef.Branch, skill.Path)
}

// RepoURL returns the GitHub repository URL.
func (skill *DiscoveredSkill) RepoURL() string {
	return fmt.Sprintf("https://github.com/%s/%s", skill.RepoRef.Owner, skill.RepoRef.Repo)
}

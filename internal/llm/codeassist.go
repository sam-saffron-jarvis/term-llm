package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/samsaffron/term-llm/internal/prompt"
)

const (
	codeAssistEndpoint             = "https://cloudcode-pa.googleapis.com"
	codeAssistAPIVersion           = "v1internal"
	googleTokenEndpoint            = "https://oauth2.googleapis.com/token"
	codeAssistProjectCacheFile     = "project.json"
	codeAssistTokenCacheFile       = "access-token.json"
	codeAssistClientCredsCacheFile = "client-creds.json"
	codeAssistProjectCacheTTL      = 24 * time.Hour
	codeAssistTokenExpiryBuffer    = 5 * time.Minute
)

// GeminiOAuthCredentials holds the OAuth credentials loaded from ~/.gemini/oauth_creds.json
type GeminiOAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiryDate   int64  `json:"expiry_date"`
}

type codeAssistProjectCache struct {
	ProjectID string `json:"project_id"`
	Platform  string `json:"platform"`
	FetchedAt int64  `json:"fetched_at"`
}

type codeAssistTokenCache struct {
	AccessToken string `json:"access_token"`
	ExpiryDate  int64  `json:"expiry_date"`
	CachedAt    int64  `json:"cached_at"`
}

type codeAssistClientCredsCache struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	SourcePath   string `json:"source_path,omitempty"`
	SourceMTime  int64  `json:"source_mtime,omitempty"`
	CachedAt     int64  `json:"cached_at,omitempty"`
}

// geminiOAuthClientCreds holds the OAuth client ID and secret
type geminiOAuthClientCreds struct {
	clientID     string
	clientSecret string
}

func loadClientCredsFromCache(debug bool) (*geminiOAuthClientCreds, bool) {
	start := time.Now()
	var cache codeAssistClientCredsCache
	if err := readCodeAssistCache(codeAssistClientCredsCacheFile, &cache); err != nil {
		logTiming(debug, "client creds cache read", start, "miss")
		return nil, false
	}
	if cache.ClientID == "" || cache.ClientSecret == "" {
		logTiming(debug, "client creds cache read", start, "empty")
		return nil, false
	}

	detail := "hit"
	if cache.SourcePath != "" && cache.SourceMTime != 0 {
		info, err := os.Stat(cache.SourcePath)
		if err != nil {
			detail = "hit-source-missing"
		} else if info.ModTime().UnixMilli() != cache.SourceMTime {
			logTiming(debug, "client creds cache read", start, "stale")
			return nil, false
		}
	}

	logTiming(debug, "client creds cache read", start, detail)
	return &geminiOAuthClientCreds{
		clientID:     cache.ClientID,
		clientSecret: cache.ClientSecret,
	}, true
}

func saveClientCredsToCache(debug bool, sourcePath string, creds *geminiOAuthClientCreds) {
	if creds == nil || creds.clientID == "" || creds.clientSecret == "" {
		return
	}

	start := time.Now()
	cache := codeAssistClientCredsCache{
		ClientID:     creds.clientID,
		ClientSecret: creds.clientSecret,
		SourcePath:   sourcePath,
		CachedAt:     time.Now().UnixMilli(),
	}

	if sourcePath != "" {
		if info, err := os.Stat(sourcePath); err == nil {
			cache.SourceMTime = info.ModTime().UnixMilli()
		}
	}

	if err := writeCodeAssistCache(codeAssistClientCredsCacheFile, &cache); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "Cache: client creds write failed: %v\n", err)
		}
		return
	}

	logTiming(debug, "client creds cache write", start, "")
}

// getGeminiOAuthClientCreds reads the OAuth client credentials, preferring cache.
func getGeminiOAuthClientCreds(debug bool) (*geminiOAuthClientCreds, bool, error) {
	if creds, ok := loadClientCredsFromCache(debug); ok {
		return creds, true, nil
	}

	creds, err := loadGeminiOAuthClientCredsFromCLI(debug)
	return creds, false, err
}

func loadGeminiOAuthClientCredsFromCLI(debug bool) (*geminiOAuthClientCreds, error) {
	loadStart := time.Now()
	// Try to find gemini-cli installation and read credentials from oauth2.js
	geminiPath, err := exec.LookPath("gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini-cli not found in PATH: %w", err)
	}

	// Resolve symlink to find actual installation
	realPath, err := filepath.EvalSymlinks(geminiPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve gemini path: %w", err)
	}

	// Navigate from dist/index.js to find oauth2.js
	// Path: .../node_modules/@google/gemini-cli/dist/index.js
	// Target: .../node_modules/@google/gemini-cli-core/dist/src/code_assist/oauth2.js
	baseDir := filepath.Dir(filepath.Dir(realPath)) // go up from dist/index.js to package root
	oauth2Path := filepath.Join(baseDir, "node_modules", "@google", "gemini-cli-core", "dist", "src", "code_assist", "oauth2.js")

	content, err := os.ReadFile(oauth2Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read oauth2.js from gemini-cli: %w", err)
	}

	logTiming(debug, "client creds load", loadStart, "gemini-cli")

	// Extract client ID and secret using simple string matching
	contentStr := string(content)

	clientID := extractConstant(contentStr, "OAUTH_CLIENT_ID")
	clientSecret := extractConstant(contentStr, "OAUTH_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("failed to extract OAuth credentials from gemini-cli")
	}

	creds := &geminiOAuthClientCreds{
		clientID:     clientID,
		clientSecret: clientSecret,
	}

	saveClientCredsToCache(debug, oauth2Path, creds)

	return creds, nil
}

// extractConstant extracts a string constant value from JS source
func extractConstant(content, name string) string {
	// Look for: const NAME = 'value';
	prefix := "const " + name + " = '"
	start := strings.Index(content, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(content[start:], "'")
	if end == -1 {
		return ""
	}
	return content[start : start+end]
}

func logTiming(debug bool, label string, start time.Time, detail string) {
	if !debug {
		return
	}
	elapsed := time.Since(start).Round(time.Millisecond)
	if detail != "" {
		fmt.Fprintf(os.Stderr, "Timing: %s (%s) %s\n", label, detail, elapsed)
		return
	}
	fmt.Fprintf(os.Stderr, "Timing: %s %s\n", label, elapsed)
}

func codeAssistCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "term-llm", "codeassist"), nil
}

func readCodeAssistCache(fileName string, dest any) error {
	cacheDir, err := codeAssistCacheDir()
	if err != nil {
		return err
	}
	cachePath := filepath.Join(cacheDir, fileName)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func writeCodeAssistCache(fileName string, src any) error {
	cacheDir, err := codeAssistCacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	cachePath := filepath.Join(cacheDir, fileName)
	return os.WriteFile(cachePath, data, 0600)
}

func codeAssistPlatform() string {
	platform := "PLATFORM_UNSPECIFIED"
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			platform = "DARWIN_ARM64"
		} else {
			platform = "DARWIN_AMD64"
		}
	case "linux":
		if runtime.GOARCH == "arm64" {
			platform = "LINUX_ARM64"
		} else {
			platform = "LINUX_AMD64"
		}
	case "windows":
		platform = "WINDOWS_AMD64"
	}
	return platform
}

// CodeAssistProvider implements Provider using Google Code Assist API with OAuth
type CodeAssistProvider struct {
	creds       *GeminiOAuthCredentials
	model       string
	projectID   string                  // cached after loadCodeAssist
	clientCreds *geminiOAuthClientCreds // cached OAuth client credentials
	// Tracks whether client credentials came from disk cache (so we can retry on failure).
	clientCredsFromCache bool
}

func NewCodeAssistProvider(creds *GeminiOAuthCredentials, model string) *CodeAssistProvider {
	return &CodeAssistProvider{
		creds: creds,
		model: model,
	}
}

func (p *CodeAssistProvider) Name() string {
	return fmt.Sprintf("Gemini Code Assist (%s)", p.model)
}

// getAccessToken returns a valid access token, refreshing if needed
func (p *CodeAssistProvider) getAccessToken(debug bool) (string, error) {
	now := time.Now().UnixMilli()
	bufferMs := int64(codeAssistTokenExpiryBuffer.Milliseconds())

	if p.creds.AccessToken != "" && (p.creds.ExpiryDate == 0 || now < p.creds.ExpiryDate-bufferMs) {
		logTiming(debug, "access token", time.Now(), "memory")
		return p.creds.AccessToken, nil
	}

	if p.loadAccessTokenFromCache(debug) {
		return p.creds.AccessToken, nil
	}

	// Ensure we have client credentials for refresh
	if p.clientCreds == nil {
		creds, fromCache, err := getGeminiOAuthClientCreds(debug)
		if err != nil {
			return "", fmt.Errorf("failed to get OAuth client credentials: %w", err)
		}
		p.clientCreds = creds
		p.clientCredsFromCache = fromCache
	}

	token, expiryDate, err := p.refreshAccessToken(debug, "")
	if err != nil && p.clientCredsFromCache {
		if debug {
			fmt.Fprintln(os.Stderr, "Cache: client creds refresh failed, reloading from gemini-cli")
		}
		creds, loadErr := loadGeminiOAuthClientCredsFromCLI(debug)
		if loadErr == nil {
			p.clientCreds = creds
			p.clientCredsFromCache = false
			token, expiryDate, err = p.refreshAccessToken(debug, "retry")
		}
	}
	if err != nil {
		return "", err
	}

	p.creds.AccessToken = token
	p.creds.ExpiryDate = expiryDate
	p.saveAccessTokenToCache(debug)

	return token, nil
}

func (p *CodeAssistProvider) refreshAccessToken(debug bool, detail string) (string, int64, error) {
	refreshStart := time.Now()
	data := url.Values{}
	data.Set("client_id", p.clientCreds.clientID)
	data.Set("client_secret", p.clientCreds.clientSecret)
	data.Set("refresh_token", p.creds.RefreshToken)
	data.Set("grant_type", "refresh_token")

	resp, err := http.Post(googleTokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	logTiming(debug, "token refresh", refreshStart, detail)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("token refresh failed: %s", string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w", err)
	}

	expiryDate := time.Now().UnixMilli() + int64(tokenResp.ExpiresIn)*1000
	return tokenResp.AccessToken, expiryDate, nil
}

func (p *CodeAssistProvider) loadAccessTokenFromCache(debug bool) bool {
	start := time.Now()
	var cache codeAssistTokenCache
	if err := readCodeAssistCache(codeAssistTokenCacheFile, &cache); err != nil {
		logTiming(debug, "access token cache read", start, "miss")
		return false
	}
	if cache.AccessToken == "" || cache.ExpiryDate == 0 {
		logTiming(debug, "access token cache read", start, "empty")
		return false
	}

	bufferMs := int64(codeAssistTokenExpiryBuffer.Milliseconds())
	now := time.Now().UnixMilli()
	if now >= cache.ExpiryDate-bufferMs {
		logTiming(debug, "access token cache read", start, "expired")
		return false
	}

	p.creds.AccessToken = cache.AccessToken
	p.creds.ExpiryDate = cache.ExpiryDate
	logTiming(debug, "access token cache read", start, "hit")
	return true
}

func (p *CodeAssistProvider) saveAccessTokenToCache(debug bool) {
	if p.creds.AccessToken == "" || p.creds.ExpiryDate == 0 {
		return
	}

	start := time.Now()
	cache := codeAssistTokenCache{
		AccessToken: p.creds.AccessToken,
		ExpiryDate:  p.creds.ExpiryDate,
		CachedAt:    time.Now().UnixMilli(),
	}
	if err := writeCodeAssistCache(codeAssistTokenCacheFile, &cache); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "Cache: access token write failed: %v\n", err)
		}
		return
	}
	logTiming(debug, "access token cache write", start, "")
}

func (p *CodeAssistProvider) loadProjectIDFromCache(debug bool, platform string) bool {
	start := time.Now()
	var cache codeAssistProjectCache
	if err := readCodeAssistCache(codeAssistProjectCacheFile, &cache); err != nil {
		logTiming(debug, "project ID cache read", start, "miss")
		return false
	}
	if cache.ProjectID == "" {
		logTiming(debug, "project ID cache read", start, "empty")
		return false
	}
	if cache.Platform != "" && cache.Platform != platform {
		logTiming(debug, "project ID cache read", start, "platform-mismatch")
		return false
	}
	if cache.FetchedAt == 0 || time.Since(time.UnixMilli(cache.FetchedAt)) > codeAssistProjectCacheTTL {
		logTiming(debug, "project ID cache read", start, "expired")
		return false
	}

	p.projectID = cache.ProjectID
	logTiming(debug, "project ID cache read", start, "hit")
	return true
}

func (p *CodeAssistProvider) saveProjectIDToCache(debug bool, platform string) {
	if p.projectID == "" {
		return
	}

	start := time.Now()
	cache := codeAssistProjectCache{
		ProjectID: p.projectID,
		Platform:  platform,
		FetchedAt: time.Now().UnixMilli(),
	}
	if err := writeCodeAssistCache(codeAssistProjectCacheFile, &cache); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "Cache: project ID write failed: %v\n", err)
		}
		return
	}
	logTiming(debug, "project ID cache write", start, "")
}

// ensureProjectID calls loadCodeAssist to get the project ID if not already cached
func (p *CodeAssistProvider) ensureProjectID(ctx context.Context, debug bool) error {
	if p.projectID != "" {
		return nil
	}

	platform := codeAssistPlatform()
	if p.loadProjectIDFromCache(debug, platform) {
		return nil
	}

	token, err := p.getAccessToken(debug)
	if err != nil {
		return err
	}

	reqBody := map[string]interface{}{
		"metadata": map[string]string{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   platform,
			"pluginType": "GEMINI",
		},
	}

	reqJSON, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:loadCodeAssist", codeAssistEndpoint, codeAssistAPIVersion)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	loadStart := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("loadCodeAssist request failed: %w", err)
	}
	defer resp.Body.Close()

	logTiming(debug, "loadCodeAssist request", loadStart, "")

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("loadCodeAssist failed with status %d: %s", resp.StatusCode, string(body))
	}

	var loadResp struct {
		CloudaicompanionProject string `json:"cloudaicompanionProject"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loadResp); err != nil {
		return fmt.Errorf("failed to parse loadCodeAssist response: %w", err)
	}

	p.projectID = loadResp.CloudaicompanionProject
	p.saveProjectIDToCache(debug, platform)
	return nil
}

func (p *CodeAssistProvider) SuggestCommands(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	if req.EnableSearch {
		return p.suggestWithSearch(ctx, req)
	}
	return p.suggestWithoutSearch(ctx, req)
}

// performSearch performs a Google Search query and returns the search context
func (p *CodeAssistProvider) performSearch(ctx context.Context, query string, debug bool) (string, error) {
	if err := p.ensureProjectID(ctx, debug); err != nil {
		return "", err
	}

	token, err := p.getAccessToken(debug)
	if err != nil {
		return "", err
	}

	searchPrompt := fmt.Sprintf("Search for current information about: %s\n\nProvide a concise summary of the most relevant and up-to-date information found.", query)

	requestInner := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": searchPrompt},
				},
			},
		},
		"tools": []map[string]interface{}{
			{"googleSearch": map[string]interface{}{}},
		},
	}

	reqBody := map[string]interface{}{
		"model":          p.model,
		"project":        p.projectID,
		"user_prompt_id": fmt.Sprintf("search-%d", time.Now().UnixNano()),
		"request":        requestInner,
	}

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Search Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Query: %s\n", query)
		fmt.Fprintln(os.Stderr, "==========================================")
	}

	reqJSON, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:generateContent", codeAssistEndpoint, codeAssistAPIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("search failed with status %d: %s", resp.StatusCode, string(body))
	}

	var genResp struct {
		Response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return "", fmt.Errorf("failed to parse search response: %w", err)
	}

	if len(genResp.Response.Candidates) == 0 || len(genResp.Response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no search results from model")
	}

	searchResult := genResp.Response.Candidates[0].Content.Parts[0].Text

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Search Response ===")
		fmt.Fprintf(os.Stderr, "Result: %s\n", searchResult)
		fmt.Fprintln(os.Stderr, "===========================================")
	}

	return searchResult, nil
}

func (p *CodeAssistProvider) suggestWithSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	// Phase 1: Perform search to get current information
	searchContext, err := p.performSearch(ctx, req.UserInput, req.Debug)
	if err != nil {
		// If search fails, fall back to suggestions without search
		if req.Debug {
			fmt.Fprintf(os.Stderr, "Search failed, falling back: %v\n", err)
		}
		return p.suggestWithoutSearch(ctx, req)
	}

	// Phase 2: Generate suggestions with search context
	if err := p.ensureProjectID(ctx, req.Debug); err != nil {
		return nil, err
	}

	token, err := p.getAccessToken(req.Debug)
	if err != nil {
		return nil, err
	}

	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, true)

	// Include search results in the user prompt
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)
	if searchContext != "" {
		userPrompt = fmt.Sprintf("%s\n\n<search_results>\n%s\n</search_results>", userPrompt, searchContext)
	}

	// Build inner request with JSON schema (no search tool - incompatible)
	requestInner := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": userPrompt},
				},
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": systemPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
			"responseSchema":   prompt.SuggestSchema(numSuggestions),
		},
	}

	reqBody := map[string]interface{}{
		"model":          p.model,
		"project":        p.projectID,
		"user_prompt_id": fmt.Sprintf("suggest-%d", time.Now().UnixNano()),
		"request":        requestInner,
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Request (with search) ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Project: %s\n", p.projectID)
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "=================================================")
	}

	reqJSON, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:generateContent", codeAssistEndpoint, codeAssistAPIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("generateContent request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("generateContent failed with status %d: %s", resp.StatusCode, string(body))
	}

	var genResp struct {
		Response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(genResp.Response.Candidates) == 0 || len(genResp.Response.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	text := genResp.Response.Candidates[0].Content.Parts[0].Text

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Response (with search) ===")
		fmt.Fprintf(os.Stderr, "Raw JSON: %s\n", text)
		fmt.Fprintln(os.Stderr, "==================================================")
	}

	var suggestions suggestionsResponse
	if err := json.Unmarshal([]byte(text), &suggestions); err != nil {
		return nil, fmt.Errorf("failed to parse suggestions JSON: %w", err)
	}

	return suggestions.Suggestions, nil
}

func (p *CodeAssistProvider) suggestWithoutSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	if err := p.ensureProjectID(ctx, req.Debug); err != nil {
		return nil, err
	}

	token, err := p.getAccessToken(req.Debug)
	if err != nil {
		return nil, err
	}

	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, false)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)

	// Build inner request
	requestInner := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": userPrompt},
				},
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": systemPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
			"responseSchema":   prompt.SuggestSchema(numSuggestions),
		},
	}

	reqBody := map[string]interface{}{
		"model":          p.model,
		"project":        p.projectID,
		"user_prompt_id": fmt.Sprintf("suggest-%d", time.Now().UnixNano()),
		"request":        requestInner,
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Project: %s\n", p.projectID)
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "==================================")
	}

	reqJSON, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:generateContent", codeAssistEndpoint, codeAssistAPIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("generateContent request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("generateContent failed with status %d: %s", resp.StatusCode, string(body))
	}

	var genResp struct {
		Response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(genResp.Response.Candidates) == 0 || len(genResp.Response.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	text := genResp.Response.Candidates[0].Content.Parts[0].Text

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Response ===")
		fmt.Fprintf(os.Stderr, "Raw JSON: %s\n", text)
		fmt.Fprintln(os.Stderr, "===================================")
	}

	var suggestions suggestionsResponse
	if err := json.Unmarshal([]byte(text), &suggestions); err != nil {
		return nil, fmt.Errorf("failed to parse suggestions JSON: %w", err)
	}

	return suggestions.Suggestions, nil
}

func (p *CodeAssistProvider) StreamResponse(ctx context.Context, req AskRequest, output chan<- string) error {
	defer close(output)

	if err := p.ensureProjectID(ctx, req.Debug); err != nil {
		return err
	}

	token, err := p.getAccessToken(req.Debug)
	if err != nil {
		return err
	}

	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	requestInner := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": userMessage},
				},
			},
		},
	}

	// Add system instruction if provided
	if req.Instructions != "" {
		requestInner["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": req.Instructions},
			},
		}
	}

	// Add Google Search tool if search is enabled
	if req.EnableSearch {
		requestInner["tools"] = []map[string]interface{}{
			{"googleSearch": map[string]interface{}{}},
		}
	}

	reqBody := map[string]interface{}{
		"model":          p.model,
		"project":        p.projectID,
		"user_prompt_id": fmt.Sprintf("ask-%d", time.Now().UnixNano()),
		"request":        requestInner,
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Code Assist Stream Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Project: %s\n", p.projectID)
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintf(os.Stderr, "Search: %v\n", req.EnableSearch)
		fmt.Fprintln(os.Stderr, "==========================================")
	}

	reqJSON, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", codeAssistEndpoint, codeAssistAPIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	streamStart := time.Now()
	headerStart := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("streamGenerateContent request failed: %w", err)
	}
	defer resp.Body.Close()

	logTiming(req.Debug, "streamGenerateContent headers", headerStart, "")

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("streamGenerateContent failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Track sources from grounding metadata
	type groundingSource struct {
		Title string
		URI   string
	}
	seenURIs := make(map[string]bool)
	var sources []groundingSource

	scanner := bufio.NewScanner(resp.Body)
	firstChunkLogged := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		if !firstChunkLogged {
			firstChunkLogged = true
			logTiming(req.Debug, "streamGenerateContent first chunk", streamStart, "")
		}

		data := line[6:]
		var chunk struct {
			Response struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
					GroundingMetadata *struct {
						GroundingChunks []struct {
							Web *struct {
								URI   string `json:"uri"`
								Title string `json:"title"`
							} `json:"web"`
						} `json:"groundingChunks"`
					} `json:"groundingMetadata"`
				} `json:"candidates"`
			} `json:"response"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Response.Candidates) > 0 {
			candidate := chunk.Response.Candidates[0]

			// Extract text
			if len(candidate.Content.Parts) > 0 {
				text := candidate.Content.Parts[0].Text
				if text != "" {
					output <- text
				}
			}

			// Collect grounding sources (deduplicated)
			if candidate.GroundingMetadata != nil {
				for _, gc := range candidate.GroundingMetadata.GroundingChunks {
					if gc.Web != nil && gc.Web.URI != "" && !seenURIs[gc.Web.URI] {
						seenURIs[gc.Web.URI] = true
						sources = append(sources, groundingSource{
							Title: gc.Web.Title,
							URI:   gc.Web.URI,
						})
					}
				}
			}
		}
	}

	// Output sources at the end if we collected any
	if len(sources) > 0 {
		output <- "\n\nSources:\n"
		for i, src := range sources {
			if src.Title != "" {
				output <- fmt.Sprintf("[%d] %s (%s)\n", i+1, src.Title, src.URI)
			} else {
				output <- fmt.Sprintf("[%d] %s\n", i+1, src.URI)
			}
		}
	}

	if firstChunkLogged {
		logTiming(req.Debug, "streamGenerateContent total", streamStart, "")
	} else {
		logTiming(req.Debug, "streamGenerateContent total", streamStart, "no chunks")
	}

	return scanner.Err()
}

// CallWithTool makes an API call with a single tool and returns raw results.
// Implements ToolCallProvider interface.
func (p *CodeAssistProvider) CallWithTool(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	if err := p.ensureProjectID(ctx, req.Debug); err != nil {
		return nil, err
	}

	token, err := p.getAccessToken(req.Debug)
	if err != nil {
		return nil, err
	}

	// Build the tool declaration
	tool := map[string]interface{}{
		"functionDeclarations": []map[string]interface{}{
			{
				"name":        req.ToolName,
				"description": req.ToolDesc,
				"parameters":  req.ToolSchema,
			},
		},
	}

	requestInner := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": req.UserPrompt},
				},
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": req.SystemPrompt},
			},
		},
		"tools": []map[string]interface{}{tool},
		"toolConfig": map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": "ANY",
			},
		},
	}

	reqBody := map[string]interface{}{
		"model":          p.model,
		"project":        p.projectID,
		"user_prompt_id": fmt.Sprintf("%s-%d", req.ToolName, time.Now().UnixNano()),
		"request":        requestInner,
	}

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: Code Assist %s Request ===\n", req.ToolName)
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Project: %s\n", p.projectID)
		fmt.Fprintf(os.Stderr, "System:\n%s\n", req.SystemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", req.UserPrompt)
		fmt.Fprintln(os.Stderr, "========================================")
	}

	reqJSON, _ := json.Marshal(reqBody)
	reqURL := fmt.Sprintf("%s/%s:generateContent", codeAssistEndpoint, codeAssistAPIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("generateContent request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("generateContent failed with status %d: %s", resp.StatusCode, string(body))
	}

	var genResp struct {
		Response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string `json:"text"`
						FunctionCall *struct {
							Name string                 `json:"name"`
							Args map[string]interface{} `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: Code Assist %s Response ===\n", req.ToolName)
		respJSON, _ := json.MarshalIndent(genResp, "", "  ")
		fmt.Fprintf(os.Stderr, "%s\n", string(respJSON))
		fmt.Fprintln(os.Stderr, "=========================================")
	}

	// Extract results from response
	result := &ToolCallResult{}
	if len(genResp.Response.Candidates) > 0 {
		for _, part := range genResp.Response.Candidates[0].Content.Parts {
			if part.Text != "" {
				result.TextOutput += part.Text
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				result.ToolCalls = append(result.ToolCalls, ToolCallArguments{
					Name:      part.FunctionCall.Name,
					Arguments: argsJSON,
				})
			}
		}
	}

	return result, nil
}

// GetEdits calls the LLM with the edit tool and returns all proposed edits.
func (p *CodeAssistProvider) GetEdits(ctx context.Context, systemPrompt, userPrompt string, debug bool) ([]EditToolCall, error) {
	return GetEditsFromProvider(ctx, p, systemPrompt, userPrompt, debug)
}

// GetUnifiedDiff calls the LLM with the unified_diff tool and returns the diff string.
func (p *CodeAssistProvider) GetUnifiedDiff(ctx context.Context, systemPrompt, userPrompt string, debug bool) (string, error) {
	return GetUnifiedDiffFromProvider(ctx, p, systemPrompt, userPrompt, debug)
}

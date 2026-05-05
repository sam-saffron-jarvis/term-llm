package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var fencedBlockRe = regexp.MustCompile("(?s)```(?:go|golang|javascript|js|node)?\\s*\\n(.*?)\\n```")
var goBenchRe = regexp.MustCompile(`BenchmarkGenerated\S*\s+\d+\s+([0-9.]+)\s+ns/op(?:\s+([0-9.]+)\s+B/op)?(?:\s+([0-9.]+)\s+allocs/op)?`)
var nodeBenchRe = regexp.MustCompile(`BENCH_RUNTIME_MS=([0-9.]+)`)

func extractCode(response string) (string, error) {
	response = strings.TrimSpace(response)
	if response == "" {
		return "", fmt.Errorf("empty response")
	}
	matches := fencedBlockRe.FindStringSubmatch(response)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1]), nil
	}
	// Accept raw code as a convenience for providers that obey "no prose" literally.
	if strings.Contains(response, "func ") || strings.Contains(response, "type ") || strings.Contains(response, "export function") || strings.Contains(response, "module.exports") {
		return response, nil
	}
	return "", fmt.Errorf("no code block found")
}

func scoreGoFunction(response string, timeout time.Duration, testBody string, imports ...string) ScoreResult {
	return scoreGo(response, timeout, false, testBody, imports...)
}

func scoreGoFunctionWithRace(response string, timeout time.Duration, testBody string, imports ...string) ScoreResult {
	return scoreGo(response, timeout, true, testBody, imports...)
}

func scoreGo(response string, timeout time.Duration, race bool, testBody string, imports ...string) ScoreResult {
	code, err := extractCode(response)
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: response}
	}
	dir, err := os.MkdirTemp("", "term-llm-codegen-bench-*")
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}
	defer os.RemoveAll(dir)

	if !strings.Contains(code, "package ") {
		code = "package main\n\n" + code
	}
	if err := os.WriteFile(filepath.Join(dir, "solution.go"), []byte(code), 0o644); err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}

	testSource := buildTestSource(testBody, imports...)
	if err := os.WriteFile(filepath.Join(dir, "solution_test.go"), []byte(testSource), 0o644); err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module benchsolution\n\ngo 1.22\n"), 0o644); err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}

	args := []string{"test", ".", "-run", "TestGenerated", "-bench", "BenchmarkGenerated", "-benchmem", "-count", "1"}
	if race {
		args = append([]string{"test", "-race"}, args[1:]...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	out := stdout.String()
	errOut := stderr.String()
	if ctx.Err() == context.DeadlineExceeded {
		return ScoreResult{Pass: false, Score: 0, Details: "scoring timed out", Stdout: out, Stderr: errOut, GeneratedCode: code}
	}
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: "tests failed", Stdout: out, Stderr: errOut, GeneratedCode: code}
	}
	metrics := parseGoBench(out)
	return ScoreResult{Pass: true, Score: 1, Details: perfSummary(out), Stdout: out, Stderr: errOut, GeneratedCode: code, Metrics: metrics}
}

func scoreNode(response string, timeout time.Duration, testSource string) ScoreResult {
	code, err := extractCode(response)
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: response}
	}
	dir, err := os.MkdirTemp("", "term-llm-codegen-bench-node-*")
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "solution.mjs"), []byte(code), 0o644); err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}
	if err := os.WriteFile(filepath.Join(dir, "solution.test.mjs"), []byte(testSource), 0o644); err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: err.Error(), GeneratedCode: code}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--test", "solution.test.mjs")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	out := stdout.String()
	errOut := stderr.String()
	if ctx.Err() == context.DeadlineExceeded {
		return ScoreResult{Pass: false, Score: 0, Details: "scoring timed out", Stdout: out, Stderr: errOut, GeneratedCode: code}
	}
	if err != nil {
		return ScoreResult{Pass: false, Score: 0, Details: "tests failed", Stdout: out, Stderr: errOut, GeneratedCode: code}
	}
	metrics := parseNodeBench(out)
	detail := "ok"
	if metrics.RuntimeMS > 0 {
		detail = fmt.Sprintf("runtime %.2f ms", metrics.RuntimeMS)
	}
	return ScoreResult{Pass: true, Score: 1, Details: detail, Stdout: out, Stderr: errOut, GeneratedCode: code, Metrics: metrics}
}

func buildTestSource(testBody string, imports ...string) string {
	seen := map[string]bool{"testing": true}
	all := []string{"testing"}
	for _, imp := range imports {
		imp = strings.TrimSpace(imp)
		if imp != "" && !seen[imp] {
			seen[imp] = true
			all = append(all, imp)
		}
	}
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n")
	for _, imp := range all {
		fmt.Fprintf(&b, "\t%q\n", imp)
	}
	b.WriteString(")\n")
	b.WriteString(testBody)
	return b.String()
}

func perfSummary(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "BenchmarkGenerated") {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return "ok"
}

func parseGoBench(out string) ScoreMetrics {
	match := goBenchRe.FindStringSubmatch(out)
	if len(match) == 0 {
		return ScoreMetrics{}
	}
	metrics := ScoreMetrics{}
	metrics.NSPerOp, _ = strconv.ParseFloat(match[1], 64)
	if len(match) > 2 {
		metrics.BytesPerOp, _ = strconv.ParseFloat(match[2], 64)
	}
	if len(match) > 3 {
		metrics.AllocsPerOp, _ = strconv.ParseFloat(match[3], 64)
	}
	if metrics.NSPerOp > 0 {
		metrics.RuntimeMS = metrics.NSPerOp / 1_000_000
	}
	return metrics
}

func parseNodeBench(out string) ScoreMetrics {
	match := nodeBenchRe.FindStringSubmatch(out)
	if len(match) == 0 {
		return ScoreMetrics{}
	}
	ms, _ := strconv.ParseFloat(match[1], 64)
	return ScoreMetrics{RuntimeMS: ms}
}

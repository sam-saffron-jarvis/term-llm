package depguard

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestInternalPackagesDoNotImportCmdPackages(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}

	modulePath, err := readModulePath(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	forbiddenPrefix := modulePath + "/cmd"
	internalDir := filepath.Join(repoRoot, "internal")
	fset := token.NewFileSet()
	var violations []string

	err = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports in %s: %w", path, err)
		}

		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import path in %s: %w", path, err)
			}
			if importPath == forbiddenPrefix || strings.HasPrefix(importPath, forbiddenPrefix+"/") {
				relPath, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					return fmt.Errorf("make %s relative to repo root: %w", path, relErr)
				}
				violations = append(violations, fmt.Sprintf("%s imports %s", filepath.ToSlash(relPath), importPath))
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("internal packages must not import cmd packages:\n%s", strings.Join(violations, "\n"))
	}
}

func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate depguard test file")
	}

	dir := filepath.Dir(file)
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat %s: %w", goModPath, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", file)
		}
		dir = parent
	}
}

func readModulePath(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}

	return "", fmt.Errorf("module declaration not found in go.mod")
}

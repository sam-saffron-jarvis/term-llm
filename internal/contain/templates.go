package contain

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:templates
var embeddedTemplates embed.FS

var builtinTemplateNames = []string{"agent", "basic"}

// BuiltinTemplateNames returns the built-in contain template names.
func BuiltinTemplateNames() []string {
	names := append([]string(nil), builtinTemplateNames...)
	sort.Strings(names)
	return names
}

// IsBuiltinTemplate reports whether name is a built-in contain template.
func IsBuiltinTemplate(name string) bool {
	for _, builtin := range builtinTemplateNames {
		if name == builtin {
			return true
		}
	}
	return false
}

type TemplateFile struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

type Template struct {
	Name       string
	Source     string
	Builtin    bool
	Descriptor *TemplateDescriptor
	Files      []TemplateFile
}

// LoadTemplate resolves a built-in, single-file, or directory template.
func LoadTemplate(ref string) (*Template, error) {
	if ref == "" {
		ref = "agent"
	}
	if IsBuiltinTemplate(ref) {
		return loadBuiltinTemplate(ref)
	}

	info, err := os.Stat(ref)
	if err != nil {
		return nil, fmt.Errorf("unknown contain template %q: use one of built-ins %s, or pass an existing compose file or template directory", ref, strings.Join(BuiltinTemplateNames(), ", "))
	}
	if info.IsDir() {
		return loadDirectoryTemplate(ref)
	}
	return loadFileTemplate(ref, info.Mode())
}

func loadBuiltinTemplate(name string) (*Template, error) {
	base := filepath.ToSlash(filepath.Join("templates", name))
	t := &Template{Name: name, Source: name, Builtin: true}
	err := fs.WalkDir(embeddedTemplates, base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := embeddedTemplates.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".template.yaml" {
			desc, err := parseTemplateDescriptor(data)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			t.Descriptor = desc
			return nil
		}
		t.Files = append(t.Files, TemplateFile{Path: rel, Data: data, Mode: info.Mode()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortTemplateFiles(t.Files)
	return t, nil
}

func loadFileTemplate(path string, mode fs.FileMode) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Template{
		Source: path,
		Files:  []TemplateFile{{Path: "compose.yaml", Data: data, Mode: mode}},
	}, nil
}

func loadDirectoryTemplate(dir string) (*Template, error) {
	var composeRel string
	for _, candidate := range []string{"compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		if info, err := os.Stat(filepath.Join(dir, candidate)); err == nil && !info.IsDir() {
			composeRel = candidate
			break
		}
	}
	if composeRel == "" {
		return nil, fmt.Errorf("template directory %q must contain compose.yaml, docker-compose.yml, or docker-compose.yaml", dir)
	}

	t := &Template{Source: dir}
	seenTargets := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if shouldSkipTemplateEntry(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".template.yaml" {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			desc, err := parseTemplateDescriptor(data)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			t.Descriptor = desc
			return nil
		}
		targetRel := rel
		if rel == composeRel || rel == "docker-compose.yml" || rel == "docker-compose.yaml" {
			targetRel = "compose.yaml"
		}
		if prev, ok := seenTargets[targetRel]; ok {
			return fmt.Errorf("template files %q and %q both map to target %q", prev, rel, targetRel)
		}
		seenTargets[targetRel] = rel

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		t.Files = append(t.Files, TemplateFile{Path: targetRel, Data: data, Mode: info.Mode()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortTemplateFiles(t.Files)
	return t, nil
}

func sortTemplateFiles(files []TemplateFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

func shouldSkipTemplateEntry(name string) bool {
	if name == ".git" || name == ".DS_Store" {
		return true
	}
	if strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".swo") {
		return true
	}
	return false
}

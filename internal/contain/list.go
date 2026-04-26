package contain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

type ListEntry struct {
	Name      string
	Status    string
	Service   string
	ConfigDir string
}

type dockerContainer struct {
	Names  string
	State  string
	Status string
	Labels map[string]string
}

// List scans contain definitions and reconciles them with labelled Docker
// containers.
func List(ctx context.Context, runner Runner, stderr io.Writer) ([]ListEntry, error) {
	defs, err := scanDefinitions()
	if err != nil {
		return nil, err
	}
	psOutput, err := DockerPS(ctx, runner, stderr)
	if err != nil {
		return nil, err
	}
	containers := parseDockerPSLines(psOutput)
	return reconcileDefinitions(defs, containers), nil
}

func PrintList(entries []ListEntry, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tSTATUS\tSERVICE\tCONFIG_DIR"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Status, e.Service, e.ConfigDir); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func scanDefinitions() ([]ListEntry, error) {
	return Definitions()
}

// Definitions scans global contain workspace definitions without consulting Docker.
func Definitions() ([]ListEntry, error) {
	root, err := ContainersRoot()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", "compose.yaml"))
	if err != nil {
		return nil, err
	}
	entries := make([]ListEntry, 0, len(matches))
	for _, compose := range matches {
		dir := filepath.Dir(compose)
		name := filepath.Base(dir)
		entry := ListEntry{Name: name, Status: "missing", Service: "app", ConfigDir: dir}
		info, err := ReadComposeInfo(compose)
		if err != nil {
			entry.Status = "invalid"
			entry.Service = "-"
		} else if info.Invalid {
			entry.Status = "invalid"
			entry.Service = "-"
		} else {
			entry.Service = info.DefaultService()
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func parseDockerPSLines(data []byte) []dockerContainer {
	var containers []dockerContainer
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		container := dockerContainer{
			Names:  stringValue(raw["Names"]),
			State:  stringValue(raw["State"]),
			Status: stringValue(raw["Status"]),
			Labels: map[string]string{},
		}
		if labels, ok := asStringMap(raw["Labels"]); ok {
			for k, v := range labels {
				container.Labels[k] = stringValue(v)
			}
		} else {
			container.Labels = parseDockerLabelString(stringValue(raw["Labels"]))
		}
		containers = append(containers, container)
	}
	return containers
}

func parseDockerLabelString(s string) map[string]string {
	labels := map[string]string{}
	if s == "" {
		return labels
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, "=", 2)
		if len(pieces) == 1 {
			labels[pieces[0]] = ""
		} else {
			labels[pieces[0]] = pieces[1]
		}
	}
	return labels
}

func reconcileDefinitions(defs []ListEntry, containers []dockerContainer) []ListEntry {
	entries := append([]ListEntry(nil), defs...)
	matchedContainers := map[int]bool{}
	for i := range entries {
		var matches []dockerContainer
		for j, container := range containers {
			if container.Labels["org.term-llm.contain.name"] == entries[i].Name || container.Labels["org.term-llm.contain.config_dir"] == entries[i].ConfigDir {
				matches = append(matches, container)
				matchedContainers[j] = true
			}
		}
		if entries[i].Status == "invalid" {
			continue
		}
		if len(matches) == 0 {
			entries[i].Status = "missing"
			continue
		}
		entries[i].Status = "stopped"
		for _, container := range matches {
			if containerRunning(container) {
				entries[i].Status = "running"
				break
			}
		}
	}

	for i, container := range containers {
		if matchedContainers[i] {
			continue
		}
		name := container.Labels["org.term-llm.contain.name"]
		if name == "" {
			name = container.Names
		}
		service := container.Labels["org.term-llm.contain.service"]
		if service == "" {
			service = "-"
		}
		configDir := container.Labels["org.term-llm.contain.config_dir"]
		if configDir == "" {
			configDir = "-"
		}
		entries = append(entries, ListEntry{Name: name, Status: "orphaned", Service: service, ConfigDir: configDir})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name == entries[j].Name {
			return entries[i].Status < entries[j].Status
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

func containerRunning(container dockerContainer) bool {
	return strings.EqualFold(container.State, "running") || strings.HasPrefix(strings.ToLower(container.Status), "up")
}

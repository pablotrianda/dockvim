package dockercli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Container struct {
	ID      string            `json:"ID"`
	Image   string            `json:"Image"`
	Command string            `json:"Command"`
	Created string            `json:"CreatedAt"`
	Status  string            `json:"Status"`
	Ports   string            `json:"Ports"`
	Names   string            `json:"Names"`
	State   string            `json:"State"`
	Labels  map[string]string `json:"-"`
}

// UnmarshalJSON handles Docker CLI versions where the ps template's Labels
// field is emitted as a comma-separated string rather than a JSON object.
func (c *Container) UnmarshalJSON(data []byte) error {
	type plain Container
	var raw struct {
		plain
		Labels json.RawMessage `json:"Labels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = Container(raw.plain)
	c.Labels = map[string]string{}
	if len(raw.Labels) == 0 || string(raw.Labels) == "null" {
		return nil
	}
	var labelMap map[string]string
	if err := json.Unmarshal(raw.Labels, &labelMap); err == nil {
		c.Labels = labelMap
		return nil
	}
	var labelText string
	if err := json.Unmarshal(raw.Labels, &labelText); err != nil {
		return nil // labels are optional metadata
	}
	for _, part := range strings.Split(labelText, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			c.Labels[key] = value
		}
	}
	return nil
}

type Project struct {
	Name       string
	Containers []Container
	Directory  string
	Compose    []string
}

func Run(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func ListContainers() ([]Container, error) {
	out, err := Run("ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var containers []Container
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c Container
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parse docker ps output: %w", err)
		}
		// docker's template labels field is a comma-separated string on some versions.
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		containers = append(containers, c)
	}
	return containers, nil
}

func ListProjects(containers []Container) []Project {
	byName := map[string]*Project{}
	order := []string{}
	for _, c := range containers {
		name := c.Labels["com.docker.compose.project"]
		if name == "" {
			continue
		}
		p, ok := byName[name]
		if !ok {
			p = &Project{Name: name}
			byName[name] = p
			order = append(order, name)
		}
		p.Containers = append(p.Containers, c)
		if p.Directory == "" {
			p.Directory = c.Labels["com.docker.compose.project.working_dir"]
		}
		if p.Compose == nil {
			configs := c.Labels["com.docker.compose.project.config_files"]
			if configs != "" {
				for _, f := range strings.Split(configs, ",") {
					f = strings.TrimSpace(f)
					if f != "" {
						p.Compose = append(p.Compose, f)
					}
				}
			}
		}
	}
	projects := make([]Project, 0, len(order))
	for _, name := range order {
		projects = append(projects, *byName[name])
	}
	return projects
}

func ComposeArgs(p Project, action string) ([]string, error) {
	if len(p.Compose) == 0 {
		return nil, fmt.Errorf("el proyecto %q no expone la etiqueta config_files; no se puede localizar el Compose", p.Name)
	}
	args := []string{"compose", "-p", p.Name}
	for _, f := range p.Compose {
		args = append(args, "-f", filepath.Clean(f))
	}
	switch action {
	case "up":
		args = append(args, "up", "-d")
	case "down":
		args = append(args, "down")
	default:
		return nil, fmt.Errorf("acción Compose desconocida: %s", action)
	}
	return args, nil
}

func ComposeAction(p Project, action string) error {
	args, err := ComposeArgs(p, action)
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", args...)
	if p.Directory != "" {
		cmd.Dir = p.Directory
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func ContainerAction(action, id string) error {
	_, err := Run(action, id)
	return err
}

func RecentLogs(id string) (string, error) {
	return Run("logs", "--tail", "100", "--timestamps", id)
}

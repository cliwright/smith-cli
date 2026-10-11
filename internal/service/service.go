// Package service loads Smith service files: the named compositions of
// repository projects that live as arbitrary-named *.yml files directly
// inside the repo's reserved services/ project root.
//
// A service has no behavior of its own. It names a set of components and
// optionally maps a closed vocabulary of service targets (build, test, lint,
// push, plan, deploy, destroy) onto component target names — direction
// component target -> service target. Execution order is never declared
// here; it comes from project-level manifest depends_on, and a service run
// is just a root-set into the ordinary targetrun closure graph.
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cliwright/smith/internal/validate"
)

// Service is the typed view of one services/*.yml file.
type Service struct {
	Version    int         `json:"version" yaml:"version"`
	Name       string      `json:"name" yaml:"name"`
	Components []Component `json:"components" yaml:"components"`
}

// Component is one project of a service. Targets maps component target
// names onto service targets ({"apply": "deploy"}: the component's apply
// target implements the service's deploy target). Without a mapping, a
// component implements a service target by name match.
type Component struct {
	Project string            `json:"project" yaml:"project"`
	Targets map[string]string `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// StandardTargets is the closed vocabulary of service targets, schema-
// enforced as the only legal mapping values. It is the display order for
// "implements" listings and the typo gate for CLI-provided target names.
var StandardTargets = []string{"build", "test", "lint", "push", "plan", "deploy", "destroy"}

// Load reads path, parses it and validates it against the embedded service
// schema.
func Load(path string) (*Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var doc any
	if err := node.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if err := validate.Validate(validate.Service, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var s Service
	if err := node.Decode(&s); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &s, nil
}

// LoadDir loads every service file directly inside dir: top-level *.yml
// entries only — smith.yml (a project manifest directly at the root) and
// subdirectories (projects inside the namespace) are not service files and
// are ignored. A missing dir means no services. Service names must be
// unique across the loaded set; every component must name a known project
// (projectNames) and no component may appear twice. Returned services are
// sorted by name.
func LoadDir(dir string, projectNames map[string]bool) ([]*Service, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var services []*Service
	seen := map[string]string{} // service name -> file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") || e.Name() == "smith.yml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := Load(path)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[s.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate service name %q (also defined in %s)", path, s.Name, prev)
		}
		seen[s.Name] = path
		if err := validateComponents(path, s, projectNames); err != nil {
			return nil, err
		}
		services = append(services, s)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services, nil
}

// validateComponents checks every component names a known project exactly
// once per service.
func validateComponents(path string, s *Service, projectNames map[string]bool) error {
	seen := map[string]bool{}
	for _, c := range s.Components {
		if !projectNames[c.Project] {
			return fmt.Errorf("%s: service %q lists unknown component %q (no project with that name in this repository)", path, s.Name, c.Project)
		}
		if seen[c.Project] {
			return fmt.Errorf("%s: service %q lists component %q twice", path, s.Name, c.Project)
		}
		seen[c.Project] = true
	}
	return nil
}

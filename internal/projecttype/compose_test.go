package projecttype

import (
	"strings"
	"testing"
)

// mkComposeType builds a minimal type for Compose table tests: the name
// carries the language/flavor/kind shape only when needed for messages; the
// disambiguator keeps names unique across table entries.
func mkComposeType(name string, version int, targets []string, params, env map[string]string, deps map[string][]string, wd WorkingDir) *ProjectType {
	typed := make(map[string]Target, len(targets))
	for _, target := range targets {
		typed[target] = Target{Steps: []string{"echo " + target}}
	}
	declared := make(map[string]Param, len(params))
	for key := range params {
		declared[key] = Param{Default: params[key]}
	}
	envCopy := make(map[string]string, len(env))
	for key := range env {
		envCopy[key] = env[key]
	}
	return &ProjectType{
		Name:         name,
		Version:      version,
		Description:  "test type",
		Tools:        []string{"sh"},
		WorkingDir:   wd,
		Targets:      typed,
		DependsOn:    deps,
		Params:       declared,
		Environment:  envCopy,
		Capabilities: nil,
	}
}

func TestComposeHappyPath(t *testing.T) {
	a := mkComposeType("mock/build", 1, []string{"build", "test"}, map[string]string{"config_path": "a.toml"}, map[string]string{"A_ONLY": "1"}, nil, WorkingDirProject)
	b := mkComposeType("mock/docs", 1, []string{"docs"}, map[string]string{"output_dir": "docs"}, map[string]string{"B_ONLY": "1"}, nil, WorkingDirProject)

	byTarget, byParam, err := Compose([]*ProjectType{a, b})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if byTarget["build"] != a || byTarget["test"] != a || byTarget["docs"] != b {
		t.Errorf("byTarget = %v, want build/test owned by a, docs by b", byTarget)
	}
	if byParam["config_path"] != a || byParam["output_dir"] != b {
		t.Errorf("byParam = %v", byParam)
	}
}

func TestComposeDisjointSingleType(t *testing.T) {
	a := mkComposeType("mock/build", 1, []string{"build"}, map[string]string{"p": "1"}, map[string]string{"E": "1"}, nil, WorkingDirRepo)
	byTarget, byParam, err := Compose([]*ProjectType{a})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if byTarget["build"] != a || byParam["p"] != a {
		t.Errorf("single-type indexes = %v, %v", byTarget, byParam)
	}
}

func TestComposeConflicts(t *testing.T) {
	tests := []struct {
		name  string
		types func() []*ProjectType
		want  string // error substring
	}{
		{
			name: "same name at two versions",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, []string{"build"}, nil, nil, nil, WorkingDirProject),
					mkComposeType("mock/build", 2, []string{"test"}, nil, nil, nil, WorkingDirProject),
				}
			},
			want: "two versions of the same type",
		},
		{
			name: "target collision",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, []string{"build"}, nil, nil, nil, WorkingDirProject),
					mkComposeType("mock/docs", 1, []string{"build"}, nil, nil, nil, WorkingDirProject),
				}
			},
			want: `both declare target "build"`,
		},
		{
			name: "param collision",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, nil, map[string]string{"config_path": "a"}, nil, nil, WorkingDirProject),
					mkComposeType("mock/docs", 1, nil, map[string]string{"config_path": "b"}, nil, nil, WorkingDirProject),
				}
			},
			want: `both declare param "config_path"`,
		},
		{
			name: "environment key collision",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, nil, nil, map[string]string{"DEBUG": "1"}, nil, WorkingDirProject),
					mkComposeType("mock/docs", 1, nil, nil, map[string]string{"DEBUG": "2"}, nil, WorkingDirProject),
				}
			},
			want: `both set environment key "DEBUG"`,
		},
		{
			name: "working_dir disagreement",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, nil, nil, nil, nil, WorkingDirProject),
					mkComposeType("mock/docs", 1, nil, nil, nil, nil, WorkingDirRepo),
				}
			},
			want: "disagree on working_dir",
		},
		{
			name: "cross-type depends_on rejected",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, []string{"build"}, nil, nil, nil, WorkingDirProject),
					mkComposeType("mock/docs", 1, []string{"docs"}, nil, nil, map[string][]string{"docs": {"build"}}, WorkingDirProject),
				}
			},
			want: "depends_on must stay within one type",
		},
		{
			name: "within-type dangling depends_on still rejected",
			types: func() []*ProjectType {
				return []*ProjectType{
					mkComposeType("mock/build", 1, []string{"build"}, nil, nil, map[string][]string{"build": {"nope"}}, WorkingDirProject),
				}
			},
			want: `"nope"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Compose(tt.types())
			if err == nil {
				t.Fatalf("Compose succeeded, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Compose error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

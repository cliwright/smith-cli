# Project types

A project type is a named, versioned bundle of targets. It is the "how" of
a project: the shell commands that build, test, lint, and deploy it.
Types are plain data — one JSON file plus optional `scripts/` and `help/`
directories — which makes them easy to write, review, and share.

Smith ships no built-in types. You install them from
[registries](registries.md), or you write your own
([Author a project type](authoring-types.md)).

## Anatomy of the name

`language/flavor/kind@version`

- `python/astral/lib@1` — a Python library, Astral toolchain (uv, ruff),
  version 1.
- `docker/bake/image@1` — a container image built with docker bake.
- `tofu/std-whoiam/infra@1` — an OpenTofu module with a whoiam-flavored
  standard workflow.

The flavor segment is always present. Use `std` when the language has one
standard toolchain. Alternatives live in the flavor, not in the kind:
bake and compose images are two flavors of `docker/*/image`, so a project
picks one and every project still means the same thing by `image`.

## project-type.json

```json
{
  "name": "python/astral/lib",
  "version": 1,
  "description": "Python library managed with uv and ruff",
  "capabilities": ["buildable", "testable", "lintable"],
  "tools": ["uv"],
  "working_dir": "project",
  "targets": {
    "build": [
      "uv build"
    ],
    "test": {
      "description": "Run the unit tests",
      "steps": [
        "uv sync",
        "uv run pytest"
      ]
    },
    "lint": [
      "uv run ruff check ."
    ]
  },
  "depends_on": {
    "test": ["build"]
  },
  "params": {
    "python_version": {
      "default": "3.12",
      "description": "Python version for the venv"
    }
  },
  "environment": {
    "PYTHON_VERSION": "{{.python_version}}"
  }
}
```

Field by field:

| Field | Required | What it does |
|---|---|---|
| `name` | yes | `language/flavor/kind`. Must match the registry directory the type lives in. |
| `version` | yes | Integer, `1` or higher. Must match the `v<N>` directory. |
| `description` | yes | One line, shown by `smith types` and target listings. |
| `capabilities` | no | Tags from a closed list (`buildable`, `testable`, `lintable`, `formattable`, `runnable`, `packageable`, `validatable`, `planable`, `applicable`, `deployable`). Informational today. |
| `tools` | yes | Executables the type's steps need. Checked by `smith doctor`; at least one. |
| `working_dir` | no | `project` (default) or `repo` — where steps run. A manifest may override it. |
| `targets` | yes | At least one. Each target is a steps array, or an object with `description` (up to 80 chars) and `steps`. |
| `depends_on` | no | Target wiring inside this type: `"test": ["build"]` runs `build` before `test`. Target names must exist in this type. |
| `params` | yes* | Parameters a project may set. Each has a required `default` and an optional `description`. |
| `environment` | no | Variables exported to steps. Values are templates. |

\* `params` is optional as a field, but every declared parameter must carry
a `default`.

A target's `description` shows up in `smith <name> list` as
`test -- Run the unit tests`, so keep it short and useful.

## Templating

Step strings, `environment` values, and parameter defaults are Go
templates with the `{{.name}}` syntax:

- `{{.python_version}}` — a parameter.
- `{{.SMITH_REPO_ROOT}}`, `{{.SMITH_PROJECT}}`, `{{.SMITH_PROJECT_DIR}}`,
  `{{.SMITH_TYPE_DIR}}`, `{{.SMITH_CLI_ARGS}}` — computed variables. See
  [Environments](environments.md).

Rules that keep templates predictable:

- A reference to an unknown name is an error. Smith renders every type at
  load, so a broken template fails at `smith sync` time, not in the middle
  of a run.
- `SMITH_TARGET` and `SMITH_ENV` are never template variables; they only
  exist in the step's environment. A param key is always lowercase, so a
  param can never shadow a `SMITH_*` name.
- Manifest parameter values render in a first pass against the computed
  variables only; everything else renders in a second pass against merged
  params plus computed variables. A param cannot reference another param.

## Multi-type composition

A project may list several types (see
[The smith.yml manifest](smith-yml.md#multi-type-projects)). Types
compose when they are disjoint — one owns `build`, another owns `env.get`
— and Smith rejects a combination that collides: duplicate refs, two
types claiming the same target, parameter key, or environment key, or
disagreement on `working_dir`. Flavors exist so alternatives never have to
share a project.

## Scripts and help pages

A type directory may ship more than the JSON file:

- `scripts/` — helpers the steps call as
  `{{.SMITH_TYPE_DIR}}/scripts/publish.sh`. Smith packs them into the
  artifact, so they travel with the type.
- `help/project.md`, `help/<target>.md` — markdown appended to the
  generated help for `smith <name> help` and
  `smith <name> <target> help`. Ship them; your users read help in the
  terminal, not in a wiki.

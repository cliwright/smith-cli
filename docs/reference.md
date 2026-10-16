# Command reference

Every smith command. Run `smith <cmd> --help` for flags; namespacing rules
are on the [Running targets](running-targets.md) page.

## Repo lifecycle

| Command | What it does |
|---|---|
| `smith init` | Seed `.smith/repo.yml` in the current directory (roots, registries, tools) |
| `smith sync` | Fetch pinned types (ingot + sidecar) into `.smith/types/`, write the lock; `--force` for a clean-slate rebuild |
| `smith doctor` | BYOT health check: probe every declared tool, report unloadable types; non-zero exit when unhealthy |

## Seeing the repo

| Command | What it does |
|---|---|
| `smith list` | Declared namespaces + the reserved `repo` namespace |
| `smith tree` | Discovered projects with type refs; `--show-deps` adds dependency edges |
| `smith types` | Installed-type browser: pinned / mismatched / detail views |
| `smith <project> list` | A project's targets, with descriptions (grouped per type when multi-type) |

## Running

| Command | What it does |
|---|---|
| `smith <project> <target>` | Run a target — dependencies first, serial, fail-fast |
| `smith <ns> <project> <target>` | Namespaced form; always unambiguous |
| `smith <target>` | Repo-wide run of a target across implementing projects |
| `smith <project> <target> --env NAME` | Select the environment for the run |
| `smith <project> <target> --context` | Print the resolved step environment (source-annotated) instead of running |
| `smith <project> <target> -- …` | Everything after `--` reaches steps as `SMITH_CLI_ARGS` |
| `smith <project> help` / `smith <project> <target> help` | Composed help: generated vars/steps + the type's `help/*.md` |

## Repo attachments & services

| Command | What it does |
|---|---|
| `smith repo` | List attachments (`repo_targets` in repo.yml) |
| `smith repo <alias> <target>` | Run a repo-level target (synthetic manifest at the repo root) |
| `smith services` | List service compositions (`services/*.yml`) |
| `smith services <name>` | Show a service's components and target mapping |
| `smith services <name> <target>` | Run a service target across its components |
| `smith services <name> graph` | Component dependency view (project-level cycle check) |
| `smith services <name> graph <target>` | Execution-order preview — builds the plan, never runs it |

Service targets are the closed vocabulary `build, test, lint, push, plan,
deploy, destroy`.

## Authoring

| Command | What it does |
|---|---|
| `smith new <language>/<kind> --version N` | Scaffold a project from a cookiecutter template |
| `smith new smith/type --version N` | Scaffold a project **type** in registry layout |
| `smith smelt --path <dir>` | Validate a type dir and pack its ingot + `.sha256` sidecar |

## Argument classification (the crispy rules)

- Two or more positionals: `<ns> <name> <target>` — or `<name> <target>` when
  the name is repo-unique; projects win over namespaces on collision.
- One positional: namespace → repo-wide target → bare project listing, in
  that order.
- `repo` and `services`/`help` are reserved keywords shadowing same-named
  directories or projects.

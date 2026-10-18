# Command reference

## Setup and maintenance

| Command | What it does |
|---|---|
| `smith init` | Create `.smith/repo.yml` (seeds roots and a registry) and an empty lock. No-op if the repo is already initialized. |
| `smith new <language>/<kind> --version N` | Scaffold a project from a cookiecutter template. Kinds: `lib`, `tool`, `service`, `image`, `infra`, `type`. `--version` is required. `type` scaffolds a project type instead of a project. |
| `smith sync [name@version …]` | Fetch types (ingot + hash sidecar) into `.smith/types/` and pin them in `.smith/lock.json`. With arguments, sync only those types. Unions fetched types' `tools` into `repo.yml`. |
| `smith sync --force` | Delete the lock and `.smith/types/`, then rebuild from scratch. Takes no type arguments. |
| `smith smelt --path <dir>` | Pack a registry-layout type directory into its ingot + sidecar, enforcing that path and file agree on name and version. |
| `smith doctor` | Probe every tool your repo needs (repo `tools` plus all type `tools`) with `<tool> --version`. Non-zero exit when anything is missing. |
| `smith version` | Print the Smith version. |

## Seeing the repo

| Command | What it does |
|---|---|
| `smith list` | Print the declared namespaces (project roots) plus the reserved `repo`. |
| `smith tree` | Print the discovered repo: roots, projects, type refs, dependencies. |
| `smith tree --show-deps` | Add a `Dependencies:` section with one line per `depends_on` entry. |
| `smith types` | List installed types with lock status (`pinned`, `not pinned`, `hash mismatch — run smith sync`). |
| `smith types <name>` | List the installed versions of one type. |
| `smith types <name>@<version>` | Full detail: description, tools, params, environment, targets, depends_on, status. |

## Running

All run commands accept `--env <name>` and `--context`.

| Command | What it does |
|---|---|
| `smith <name>` | List one project's targets (`name -- description` lines). |
| `smith <name> <target>` | Run a target on one project, dependencies first. Works with or without the namespace: `smith libs auth test` ≡ `smith auth test`. |
| `smith <name> help` / `smith <name> <target> help` | Show generated help (variables, rendered steps) plus the type's `help/*.md`, via `glow` and a pager when available. |
| `smith <ns> <target>` | Run a target on every project in the namespace that declares it; others are skipped and reported. |
| `smith <target>` | Run a target repo-wide, in dependency order. |
| `smith <name> <target> -- <args…>` | Pass everything after `--` to the steps as `$SMITH_CLI_ARGS` (also `{{.SMITH_CLI_ARGS}}`). |
| `smith repo` / `smith repo list` | List attachment aliases. |
| `smith repo <alias> <target>` | Run an attachment (repo-level target). |
| `smith services` / `smith services list` | List service definitions. |
| `smith services <name>` | Show one service's components and their targets. |
| `smith services <name> <target>` | Run a service target (`build`, `test`, `lint`, `push`, `plan`, `deploy`, `destroy`) across components. |
| `smith services <name> graph` | Show the component dependency graph. |
| `smith services <name> graph <target>` | Preview the execution order for a service target; builds the plan but never runs it. |
| `smith --context` · `smith <name> --context` · `smith <name> <target> --context` | Print the resolved environment for the repo, a project, or a target instead of running. |

## Global flags

| Flag | Effect |
|---|---|
| `--env <name>` | Select the environment. Beats `$SMITH_ENV` and `smith_env.default`. |
| `--context` | Show the resolved environment for the addressed scope. |
| `--parallel N` | Accepted for future use; today Smith warns and runs serially. |

## Rules the CLI follows

- All output goes to stdout; runs are serial and fail-fast.
- Errors describe themselves and suggest the fix (`smith list`, `smith
  sync`); Smith never dumps a usage tree for runtime errors.
- Reserved words: `repo`, `services` (namespaces), `list`, `help`
  (grammar positions), `graph` (inside `services <name>`). Command names
  beat same-named projects.
- Exact matches only; no fuzzy guessing.

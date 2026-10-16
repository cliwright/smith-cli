# Authoring project types

Types are how behavior is shared. If your org builds Python libs one way, you
write `python/<flavor>/lib@1` once and every lib project in every repo gets
`build`, `test`, `lint` for free — versioned, hash-pinned, and upgradeable on
your schedule.

## A type in 30 lines

```json
{
  "name": "python/astral/lib",
  "version": 1,
  "description": "Python library on the Astral toolchain (uv, ruff)",
  "tools": ["python", "uv"],
  "working_dir": "project",
  "params": {
    "config_path": { "default": "pyproject.toml", "description": "ruff config location" }
  },
  "targets": {
    "lint":   { "description": "Run ruff",   "steps": ["uv run ruff check --config '{{.config_path}}' ."] },
    "test":   { "description": "Run pytest", "steps": ["uv run pytest -c '{{.config_path}}'"] },
    "build":  { "description": "Build sdist/wheel", "steps": ["uv build"] }
  },
  "depends_on": { "build": ["lint", "test"] }
}
```

Field essentials:

- `name` — `language/flavor/kind`. Flavor is **mandatory**: `std` when the
  language has one standard toolchain, otherwise an org-unique string
  (`astral`, `aws-trivy`). Versions are immutable; a change means a new
  version directory.
- `tools` — BYOT declaration, checked by `smith doctor`.
- `working_dir` — `project` (default) or `repo`.
- `targets` — name → steps (bare array or `{description ≤80 chars, steps}`).
  Steps are `sh -c` command strings, Go templates with `missingkey=error`
  (dry-rendered at load). One dot segment is allowed in target names for
  tooling types (`env.get`).
- `depends_on` — target dependency map, validated at load. Never cross-type.
- `params` / `environment` — string knobs. Params need defaults; manifests
  may override but not declare.
- `capabilities` — informational enum (`buildable`, `testable`, …).

The reserved template vars: `SMITH_REPO_ROOT`, `SMITH_PROJECT`,
`SMITH_PROJECT_DIR`, `SMITH_TYPE_DIR` (the owning type's installed dir —
use it to reach type-shipped `scripts/`), and `SMITH_CLI_ARGS`. `$SMITH_ENV`
and `$SMITH_TARGET` are subprocess-only — reference them as shell vars.

## Scaffold it

```sh
smith new smith/type --version 1
```

Prompts for the name segments and renders
`<language>/<flavor>/<kind>/v<N>/project-type.json` into a `types/` root
(appended to `project_roots` on first use). Smith post-checks the result and
prints the next step. (The output is a type, not a project — no `smith.yml`.)

## The dev loop: local registry, no publish needed

Point your repo at a local registry tree and consume your own type
immediately:

```yaml
# .smith/repo.yml
registries:
  - name: local
    types:
      path: types
```

```sh
# iterate:
$EDITOR types/python/acme/lib/v1/project-type.json
smith smelt --path types/python/acme/lib/v1    # validate + pack ingot + sidecar
smith sync                                     # install into .smith/types/
```

Then inspect what you built: `smith types python/acme/lib@1`,
`smith <project> list`, `smith <project> <target> help`, and
`smith <project> <target> --context`.

## Help pages

Ship `help/project.md` (project-level help) and `help/<target>.md` (one per
target) next to `project-type.json`. Smith composes generated content —
resolved params, rendered steps, vars used — with your markdown for
`smith <project> help` and `smith <project> <target> help`. Missing pages are
fine; write them for anything with a non-obvious env model or flags.

## Publish

1. `smith smelt --path <type-dir>` — packs `<flat-name>.ingot` (project-type.json
   + `scripts/` + `help/`) and writes its `.sha256` sidecar; enforces that
   name/version in the file match the registry position.
2. Move the version tree into the registry repo; run its `task publish`.
3. Consumers `smith sync`. Custody is verified at load:
   **sidecar → lock → installed ingot + unpacked tree**; any mismatch is a
   hard error pointing at `smith sync`.

## Conventions that keep multi-type repos sane

- **Targets are verbs.** Collisions across a manifest's type list fail loudly
  at load — compatible types own `scan`/`env.get`, never `build`.
- **Steps are POSIX `sh`.** No bashisms; portable types run anywhere.
- **Fail early.** Dry-render catches template typos at load;
  `${VAR:?message}` catches missing runtime selectors before the tool runs.
- **Data variation = params; structural variation = new flavor.** No
  per-project target overrides, on purpose.

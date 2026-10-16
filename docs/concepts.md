# Core concepts

Five nouns explain almost everything Smith does: **repo**, **project**,
**type**, **target**, **registry**. One verb — *run* — plus a custody chain
that keeps behavior honest.

## Repo

The unit of Smith operation: a directory containing `.smith/repo.yml`. Every
smith command resolves upward to the nearest repo root. The repo config
declares:

- `workspace.project_roots` — where Smith looks for projects (no recursive
  `node_modules` walks; only declared roots are traversed)
- `registries` — ordered sources for types and templates (`git:` GitHub URLs
  or local `path:` trees, first match wins)
- `repo_targets` — optional repo-level attachments (see below)
- `tools`, `dotenv`, `smith_env` — repo-wide knobs

## Project

A directory under a declared root containing a `smith.yml`:

```yaml
version: 1
name: payments                 # unique across the repo
type: python/astral/lib@1      # what it is (string, or an array — see below)
working_dir: project           # optional: where steps run (project|repo)
dotenv: true                   # optional: load this project's dotenv/ dir
params:                        # optional: overrides for type-declared params
  config_path: ci/pyproject.toml
environment:                   # optional: extra env for this project's steps
  PYTHONWARNDEFAULTENCODING: "1"
depends_on:                    # optional: project-level dependencies
  - auth                       # sugar: auth's build before my build
  - project: database
    targets: [test, lint]      # run these on the dependency…
    before: [build]            # …before these run here (default: [build])
```

The manifest declares identity and dependencies — never build commands.
*What*, not *how*.

**Multi-type projects.** `type:` may be a list:

```yaml
type:
  - python/astral/lib@1
  - repo/std/psenv@1     # adds env.get / env.put tooling targets
```

All targets from all listed types are available on the project. Target name
collisions across the types are a **loud error at load** — by design, because
targets are verbs: a compatible type solves a different problem (`scan`,
`env.get`), it doesn't re-litigate `build`. `SMITH_TYPE_DIR` always points at
the *owning* type's installed directory, so type-shipped scripts resolve
correctly.

## Type

A versioned bundle of behavior, distributed as `project-type.json`:

```json
{
  "name": "python/astral/lib",
  "version": 1,
  "tools": ["python", "uv"],
  "targets": {
    "lint": { "description": "…", "steps": ["uv run ruff check ."] },
    "test": ["uv run pytest"],
    "build": ["uv build"]
  },
  "depends_on": { "build": ["lint", "test"] }
}
```

Steps are Go templates (`{{.param}}`, `{{.SMITH_REPO_ROOT}}`, …) rendered
against declared params and reserved computed vars; `missingkey=error` means
typos fail at load, not mid-run. Types may also declare `params` (string
knobs with defaults) and `environment` (env vars for steps).

Authoring types is a first-class workflow — see [Authoring project types](authoring-types.md).

## Target

A named verb on a project (`build`, `test`, `plan`, `env.get`). Running a
target means: run its dependencies (type-level `depends_on` first, then
project-level), then each step in order via `sh -c`. First non-zero exit
fails the run; skipped projects are reported, not hidden.

Two conveniences target names support:

- **One dot segment** — tooling types namespace targets (`state.list`,
  `env.get`); the dotted name is an opaque single token everywhere.
- **Descriptions** — types annotate targets (`≤80` chars); projects list them
  via `smith <project> list`.

## Registry and the custody chain

Types ship in registries as **ingots**: a packed tar of `project-type.json` +
`scripts/` + `help/`, plus a `.sha256` sidecar. `smith sync` downloads the
ingot + sidecar (exactly two GETs per type for GitHub sources), verifies the
hash, records it in `.smith/lock.json`, and unpacks into
`.smith/types/<name>@v<N>/`.

The chain of custody is enforced at load, and a mismatch anywhere is a loud
error pointing at `smith sync` — never a silent content swap:

```
sidecar (publisher) → lock (sync) → installed ingot + unpacked tree (load)
```

Version must match in path and file; a published type is immutable (a change
means a new version). Registries are ordered in `repo.yml` — a local `path:`
tree can shadow a remote one, which is the development escape hatch.

## Repo-level attachments (`repo` namespace)

`repo_targets` in repo.yml bind aliases to `repo/<flavor>/<kind>@<version>`
types — synthetic manifests whose directory is the repo root, run through the
ordinary target machinery. They answer "commands that operate on the repo as
a whole" (a uv workspace sync, a docs build):

```yaml
repo_targets:
  python:
    type: repo/uv/workspace@1
```

```sh
smith repo python sync
```

`repo` is a reserved namespace; bare `smith <target>` never includes
attachments.

## Services

A service is pure composition — a named bundle of projects, not a type. A
`services/<name>.yml` file lists components and optionally maps their targets
onto the closed service vocabulary (`build, test, lint, push, plan, deploy,
destroy`):

```yaml
version: 1
name: shop
components:
  - project: api
  - project: worker
    targets: { deploy: push }   # component's `push` implements service `deploy`
```

```sh
smith services shop build     # runs build on the component set, deps first
smith services shop graph     # the component dependency view
```

Ordering always comes from project-level `depends_on`; `services` is a
reserved namespace shadowing any directory literally named `services`.

## Where to next

- [Running targets](running-targets.md) — the command grammar in detail
- [Environments & dotenv](environments.md) — the `--env` model
- [Authoring project types](authoring-types.md) — write your own behavior

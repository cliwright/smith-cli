# Environments

Smith gives every step a fully resolved environment. Three pieces build it:
the `SMITH_*` variables, the `--env` selector, and optional dotenv files.
This page covers all three.

## The `SMITH_*` variables

| Variable | Holds | In `{{.templates}}` | In step env |
|---|---|---|---|
| `SMITH_REPO_ROOT` | Absolute path of the repo root | yes | yes |
| `SMITH_PROJECT` | Project name | yes | yes |
| `SMITH_PROJECT_DIR` | Absolute path of the project directory | yes | no |
| `SMITH_TYPE_DIR` | Absolute path of the installed type that owns the running target | yes | yes |
| `SMITH_TARGET` | Name of the running target | no | yes |
| `SMITH_ENV` | Selected environment, when one is set | no | yes |
| `SMITH_CLI_ARGS` | Arguments after `--`, empty when none | yes | yes |
| `SMITH_PAGER` | Pager command for help output | no | no |

The distinction matters. Template variables work inside type step strings
and environment values: `cd {{.SMITH_TYPE_DIR}} && ./scripts/check.sh`.
Step env variables arrive as real environment variables in the shell that
runs the step: `$SMITH_TARGET`, `$SMITH_ENV`. `SMITH_PAGER` is neither —
Smith reads it from its own process to decide how to page help output.

`SMITH_TYPE_DIR` points at the installed copy of the type that owns the
target (`.smith/types/python/astral/lib@v1/` on disk), so steps can run
scripts the type ships. On a multi-type project it is the owning type's
directory, per target.

## What wins: the precedence chain

When the same key appears in several places, the later layer wins. Weakest
to strongest:

1. Your shell environment (`os.environ`)
2. Repo dotenv base file
3. Repo dotenv overlay for the selected environment
4. Project dotenv base file
5. Project dotenv overlay for the selected environment
6. Type `environment` entries (each type, in declaration order)
7. Manifest `environment` entries
8. The computed `SMITH_REPO_ROOT`, `SMITH_PROJECT`, `SMITH_TARGET`,
   `SMITH_TYPE_DIR`, `SMITH_CLI_ARGS`
9. `SMITH_ENV`, set last, when an environment is selected

Layers 2–5 exist only when you opt in; see below. Layers 6–7 render as
templates over params and the `SMITH_*` variables, and they exist so types
and manifests can set values a developer's shell cannot shadow by accident.

## Selecting an environment: `--env`

`--env` picks a named environment, such as `dev` or `prod`. It drives the
dotenv overlay files and is exported to steps as `$SMITH_ENV`.

Resolution order, first hit wins:

1. The `--env` flag
2. The `$SMITH_ENV` variable in your shell
3. `smith_env.default` in `.smith/repo.yml`

So `smith deploy --env prod` beats everything; without the flag, `SMITH_ENV=prod smith deploy` does the same; with neither, the repo default
applies. Names match `^[a-z][a-z0-9_-]*$` (lowercase letters, digits,
underscores, hyphens). When no environment is selected, no overlay loads
and `$SMITH_ENV` stays unset — a run without `--env` behaves exactly as it
always has.

```yaml
# .smith/repo.yml
smith_env:
  default: dev
```

## The `.env` integration: `dotenv/`

Environment files are off by default. Turn them on per level with
`dotenv: true` — in `.smith/repo.yml` for the whole repo, in a
`smith.yml` for that project, or both.

When enabled, Smith reads only the `dotenv/` directory at that level
(repo root or project directory), never any other `.env` file:

```
dotenv/.env          # base file, always loaded
dotenv/.dev.env      # overlay, loaded when the selector is "dev"
dotenv/.prod.env     # overlay, loaded when the selector is "prod"
```

The base file loads first; the overlay for the selected environment loads
on top of it. With repo and project both enabled, the project files win —
per the chain above. A typical split: credentials and endpoints in the
repo base, per-project overrides in the project files, secrets for a
specific environment in the overlays.

Parsing is strict, on purpose:

- Every line is `KEY=VALUE`, a blank line, or a `#` comment. Anything else
  is an error that names the file and line.
- Keys match `[A-Za-z_][A-Za-z0-9_]*`. Duplicate keys are an error.
- Values are literal. One matching pair of surrounding quotes is stripped;
  there is no interpolation or expansion.
- Enabling dotenv without the base file is an error before any step runs.
- Selecting an environment with no overlay at any enabled level is an
  error before any step runs. (An overlay at one level is enough.)

These checks run up front for the whole plan, so a bad file fails the run
before half your projects have built.

## Inspecting the result: `--context`

`--context` shows the environment a run would use, without running
anything:

```sh
smith --context                 # repo level
smith payments --context        # one project
smith payments test --context   # one target
```

Output is sorted `KEY=VALUE` lines, each annotated with the layer that
won:

```text
DATABASE_URL=postgres://localhost/shop  # dotenv/.env
DATABASE_URL=postgres://dev.db/shop     # dotenv/.dev.env
SMITH_ENV=dev                           # --env
SMITH_PROJECT=payments                  # computed
SMITH_TARGET=test                       # computed
```

It uses the same selector resolution, param merging, and error rules as a
real run — if your dotenv files would fail, `--context` fails the same
way. Note that it shows the resolved environment, not your whole shell:
variables that only exist in your OS environment do not appear.

## A complete example

```yaml
# .smith/repo.yml (excerpt)
dotenv: true
smith_env:
  default: dev
```

```
# dotenv/.env
REGISTRY=registry.example.com
```

```
# dotenv/.prod.env
REGISTRY=registry.example.com
PUSH_POLICY=--push
```

```yaml
# images/api/smith.yml (excerpt)
dotenv: true
environment:
  IMAGE: api
```

```sh
smith api-image push --env prod
```

The step sees `REGISTRY` from the repo overlay, `PUSH_POLICY` from the
prod overlay, `IMAGE` from the manifest, `$SMITH_ENV=prod`, and the
computed `SMITH_*` paths. The same command without `--env` uses the `dev`
default, and `PUSH_POLICY` stays unset.

# Environments & dotenv

Smith has a single, repo-wide notion of an **environment** — a named overlay
(`dev`, `int`, `prd`, …) that you select once and every step in the run sees
as `$SMITH_ENV`. Infrastructure types use the same selector to pick tfvars
files and backend configs; application types use it for dotenv overlays.

## Selecting an environment

Resolution order — first hit wins:

1. `--env NAME` on the command
2. the `$SMITH_ENV` process environment
3. `smith_env.default` in `.smith/repo.yml`

```sh
smith spam plan --env dev      # per-command
export SMITH_ENV=dev           # per-shell
```

```yaml
# .smith/repo.yml — the repo-wide fallback
smith_env:
  default: dev
```

`$SMITH_ENV` is exported to every step's subprocess. It is deliberately a
**subprocess-only** variable — type steps reference it as `$SMITH_ENV`, never
as a template `{{.SMITH_ENV}}`. (Same for `$SMITH_TARGET`.)

## Dotenv files

Dotenv loading is **opt-in per level**, and reads only the conventional
`dotenv/` directory:

```yaml
# .smith/repo.yml
dotenv: true          # repo level: loads for every project
```

```yaml
# a project's smith.yml
dotenv: true          # project level: loads for this project only
```

For each enabled level, with environment `dev` selected:

| File | Loads when |
|---|---|
| `dotenv/.env` | always |
| `dotenv/.dev.env` | `dev` selected |

Rules that keep this predictable:

- Parsing is strict: malformed lines and duplicate keys are hard errors.
- An enabled level **must** have its base `dotenv/.env`; a selected
  environment **must** have its overlay at every enabled level — both are
  hard errors before any step runs.
- Precedence, low → high: OS env < repo base < repo overlay < project base <
  project overlay < type `environment` < manifest `environment` < the computed
  `SMITH_*` vars.

## Inspecting the result

`--context` prints the exact environment a run would receive — sorted,
source-annotated `KEY=VALUE` lines — and exits:

```sh
smith spam plan --env dev --context
```

Use it to answer "where does this value come from?" without guessing. It
honors the full resolution chain: selector, dotenv opt-ins, precedence, and
the hard-error policy.

## Worked example: environment-driven infrastructure

A tofu type keys everything off `$SMITH_ENV`:

```json
"plan": ["tofu plan {{if .SMITH_CLI_ARGS}}{{.SMITH_CLI_ARGS}}{{else}}--var-file=environments/$SMITH_ENV.tfvars{{end}}"]
```

With `environments/dev.tfvars` and `environments/dev.backend.tfvars` in the
project, `smith spam plan --env dev` plans dev — and `smith spam plan --env
prd` first re-points the backend (a `reconfigure` dependency), then plans
prd. One selector, whole repo.

Types that require an environment should guard it explicitly, so a missing
selection fails before the tool runs:

```sh
tofu init --backend-config="environments/${SMITH_ENV:?no environment selected}.backend.tfvars"
```

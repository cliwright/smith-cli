# Introduction

Smith is a build and orchestration tool for monorepos. You mark a few
directories as project roots and put a `smith.yml` in each project; Smith
finds the projects, builds the dependency graph, and runs targets like
`build`, `test`, and `deploy` in dependency order. It drives any toolchain:
if a tool runs on your `PATH`, Smith can run it.

Smith does not replace your tools. It runs the commands your tools already
provide, in the right order, with the right environment, from one command
surface.

## How Smith sees your repo

Two ideas drive everything:

**A project is a directory with a `smith.yml`.** The manifest declares what
the project is: its name, its type, its dependencies, its parameter values.
It never contains build commands.

```yaml
# libs/payments/smith.yml
version: 1
name: payments
type: python/astral/lib@1
```

**Behavior comes from versioned project types.** A type such as
`python/astral/lib@1` or `tofu/std-whoiam/infra@1` is a named, versioned
bundle of targets. Each target is an ordered list of plain shell commands.
Types come from registries as packed, hash-checked artifacts, and your repo
pins the exact version it uses.

```
smith.yml says:      "I am payments, a python/astral/lib@1"
project-type.json says: build = these steps, test = these steps
```

## What Smith gives you

- **Discovery.** Smith walks only the roots you declare in
  `.smith/repo.yml` and finds every project. No `node_modules` crawls.
- **A dependency graph.** Project `depends_on` entries and type-level target
  dependencies merge into one graph. Smith runs targets in topological
  order and stops at the first failure.
- **One command surface.** `smith build` builds every project that has a
  `build` target, wherever it lives, in the right order. The same grammar
  addresses one project, one namespace, or the whole repo.
- **Environments.** An `--env` selector picks dotenv overlays and reaches
  steps as `$SMITH_ENV`, so the same target can target dev or prod.
- **Honest types.** Every type carries a hash from the registry to your lock
  file to the installed copy. A changed or corrupted type is a loud error,
  never a silent swap.

## Who Smith is for

- Platform teams who ship shared libraries, services, images, and
  infrastructure from one repo and want one way to build and deploy them.
- Teams who want "how we build things" written down as versioned, testable
  types instead of wiki pages.
- Anyone with a polyglot monorepo who is tired of maintaining a Makefile
  per project.

## Where to next

- [Getting started](getting-started.md) — a working repo in five commands
- [Core concepts](concepts.md) — the mental model in one page
- [Bring your own tools](bring-your-own-tools.md) — why Smith installs nothing
- [Command reference](reference.md) — every command, one line each

# Introduction

Smith is a build and orchestration tool for monorepos that refuses to pick a
language for you. Bazel wants you to learn Starlark, Nx and Turborepo want
your repo to be JavaScript, Tilt and Earthly want to be your dev platform.
Smith wants none of that. It does one thing: **projects declare what they
are, and Smith knows how they behave.**

## The two ideas that drive everything

**1. A project is a directory with a `smith.yml`.**

```yaml
version: 1
name: payments
type: python/astral/lib@1
```

That's a whole project declaration. The manifest says what the project *is*
(name, type, dependencies, parameter values). It never contains build logic.

**2. Behavior comes from versioned project types.**

A type (`python/astral/lib@1`, `docker/bake/image@1`, `tofu/std-whoiam/infra@1`)
is a named, versioned bundle of *targets* — `build`, `test`, `lint`, `plan`,
`deploy` — where each target is an ordered list of plain shell commands.
Types are distributed through registries as packed, hash-verified artifacts,
and pinned per project.

If you can write it in a shell script, you can make it a Smith target. Smith
brings what shell scripts can't: repo-wide [discovery](concepts.md), a
[dependency graph](concepts.md) with topological ordering,
[environment selection](environments.md), and one consistent command surface.

## BYOT: bring your own tools

Smith never installs anything. A type declares its `tools` (`uv`, `docker`,
`tofu`, …); `smith doctor` checks they're on `$PATH`. Your toolchain, your
versions, your responsibility — Smith just orchestrates it.

## A tour in five commands

```sh
smith init                     # seed .smith/repo.yml, declare project roots
smith new python/lib --version 1   # scaffold a project from a template
smith sync                     # fetch pinned types into .smith/types/
smith tree                     # see everything Smith discovered
smith payments test            # run a target — dependencies first
```

## Who Smith is for

- Teams with polyglot monorepos who want one way to build, test, and ship.
- Platform teams who want to encode *how we build things* into shareable,
  versioned types instead of wiki pages.
- Infrastructure teams who want `plan`/`apply` for tofu or terraform modules
  to live in the same graph as the services they deploy.

## Where to next

- [Getting started](getting-started.md) — install Smith and run your first target
- [Core concepts](concepts.md) — the mental model: repos, projects, types, registries
- [Command reference](reference.md) — every command, one line each

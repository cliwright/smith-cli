# Getting started

## Install

Install instructions are on the way. Smith is a single static binary: one
executable, no daemon, no network access except when it fetches types from
a registry.

## A repo in five commands

```sh
mkdir smith-shop && cd smith-shop
smith init
```

`smith init` writes `.smith/repo.yml`. It seeds four project roots —
`libs`, `tools`, `services`, `images` — and one registry that points at the
public type and template repos. Edit the roots later to match your layout;
Smith only looks where you tell it to.

```sh
smith new python/lib --version 1
```

`smith new` asks a few questions and scaffolds a project into `libs/`. The
template writes the project's `smith.yml`, which points at the project type
`python/astral/lib@1`.

```sh
smith sync
```

`smith sync` fetches that type: a packed artifact (an *ingot*) plus a hash
sidecar. It installs the type into `.smith/types/` and pins its hash in
`.smith/lock.json`. Check both in. A colleague who clones the repo can build
without running `sync` first.

```sh
smith tree
```

```text
smith-shop
├── libs
│   └── payments      (python/astral/lib@1)
```

```sh
smith payments list    # what can I run?
smith payments test    # run it
```

```text
==> payments:test
$ uv run pytest
...
```

Targets run their steps with `sh`, in the project directory, in dependency
order. A failing step stops the run.

Add a second project that depends on the first, and Smith orders the graph
for you:

```yaml
# services/shop-api/smith.yml
version: 1
name: shop-api
type: python/astral/lib@1
depends_on:
  - payments        # payments' build runs before this project's build
```

```sh
smith shop-api build     # builds payments first, then shop-api
smith build              # every project with a build target, in order
```

## Key concepts

**Project.** A directory with a `smith.yml`. The manifest declares the
project's name, type, dependencies, and parameter values. It holds no
build commands.

**Project type.** A named, versioned bundle of targets, identified as
`language/flavor/kind@version` (for example `python/astral/lib@1`). Types
live in registries and install into `.smith/types/`.

**Target.** A named list of shell commands inside a type, such as `build`,
`test`, or `deploy`. You run targets, never individual steps.

**Registry.** A source of types and templates. Your repo lists its
registries in `.smith/repo.yml`, most remote first. `smith sync` fetches
through them in order.

**Lock.** `.smith/lock.json` pins the hash of every type your repo uses.
Smith checks the installed copy against the lock on every run. A mismatch
stops the run and points you at `smith sync`.

**Tool.** An executable a type needs, such as `uv` or `docker`. Smith never
installs tools; it expects them on your `PATH`. See
[Bring your own tools](bring-your-own-tools.md).

## Where to next

- [Core concepts](concepts.md) for the full model: namespaces, services,
  repo targets, multi-type projects
- [Running targets](running-targets.md) for the command grammar
- [Environments](environments.md) for `--env`, dotenv files, and `--context`

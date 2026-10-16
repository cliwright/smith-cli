# Getting started

## Install Smith

**With Go:**

```sh
go install github.com/cliwright/smith/cmd/smith@latest
```

**From source:**

```sh
git clone git@github.com:cliwright/smith-cli.git
cd smith-cli
go build -o bin/smith ./cmd/smith   # or: task build
bin/smith version                   # put bin/ on PATH if you like
```

Smith is a single static binary. It needs no daemon, no daemon socket, and no
network except when syncing types from a remote registry.

## Your first repo

```sh
mkdir smith-shop && cd smith-shop
smith init
```

`smith init` writes `.smith/repo.yml` seeded with the conventional project
roots (`libs`, `tools`, `services`, `images`) and a starter registry
configuration. Edit `workspace.project_roots` to match how your repo is laid
out — Smith only ever looks where you tell it to.

## Your first project

```sh
smith new python/lib --version 1
# answer the prompts — the project lands in libs/<name>/
```

`smith new` renders a [cookiecutter template](authoring-templates.md), writes
the project's `smith.yml`, appends the project root to `repo.yml` if needed,
and records the new project's type in the sync set.

## Sync and see

```sh
smith sync     # fetch pinned types: ingot + hash sidecar per type
smith tree     # the discovered repo
```

The lock file `.smith/lock.json` and the unpacked types under `.smith/types/`
are meant to be **checked in** — anyone who clones your repo builds with zero
setup. (Prefer a per-repo cache? Gitignore `.smith/types/` instead; Smith
reads either, and `smith sync` rebuilds.)

## Run a target

```sh
smith tree --show-deps    # what depends on what
smith <project> list      # this project's targets, with descriptions
smith <project> test      # run it — project dependencies first
smith <project> test --env dev   # with an environment selected
```

Targets run their steps in order via `sh -c`, in the project directory, and
stop at the first failure. Namespaces are optional when names are unique —
see [Running targets](running-targets.md) for the full grammar, including
services and repo-level attachments.

## Check your toolbox

```sh
smith doctor
```

`doctor` probes every tool declared by your repo's types (`--version`, BYOT —
Smith never installs anything) and reports what's missing or broken.

## Where to next

- [Core concepts](concepts.md) to understand what just happened
- [Environments & dotenv](environments.md) for `--env`, dotenv overlays, and `--context`
- [Passing arguments (--)](cli-args.md) to hand extra flags to underlying tools

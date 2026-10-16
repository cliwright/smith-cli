# Smith

**A language-agnostic build and orchestration tool for monorepos.**

Smith brings the good parts of a monorepo build system — discovery, a
dependency graph, scheduling, one command surface — to *any* toolchain, in
*any* language, without adopting a new build language or framework.

Projects declare what they are. Smith knows how they behave.

```sh
$ smith tree
smith-shop
├── apps
│   └── api            python/astral/service@1
├── libs
│   ├── auth           python/astral/lib@1
│   └── payments       python/astral/lib@1
└── images
    └── api-image      docker/bake/image@1

$ smith payments test        # runs the type's test steps, deps first
$ smith api build            # builds api and everything it depends on
$ smith build                # builds every project in the repo, in order
```

## Why Smith

Monorepo tools usually demand you adopt *their* world: a build language
(Bazel, Buck), a package-manager lock-in (Nx, Turborepo), or an ecosystem
(Tilt, Earthly). Smith's bet is different:

- **Behavior comes from versioned, shareable project types.** A directory
  with a `smith.yml` is a project; its type (`python/astral/lib@1`,
  `docker/bake/image@1`, `tofu/std-whoiam/infra@1`) defines targets like
  `build`, `test`, `lint`, `plan`, `deploy` as plain shell steps.
- **BYOT — bring your own tools.** If it's on `$PATH`, Smith can use it.
  Smith never installs anything; `smith doctor` tells you what's missing.
- **The dependency graph is data, not configuration.** `depends_on` in
  project manifests merges with type-level target dependencies; Smith
  topologically orders, fails fast, and tells you what it skipped.
- **Environments are first-class.** An `--env` selector drives dotenv
  overlays and is exported to steps as `$SMITH_ENV` — the same switch your
  infra types use to pick tfvars files and backend configs.

## How it works

```
┌────────────┐   type ref    ┌──────────────────┐   registry    ┌─────────────┐
│  smith.yml  │ ─────────────▶│ project-type.json │ ◀────────────│  ingot +    │
│ (declares)  │               │ (defines targets) │   sync       │  .sha256    │
└────────────┘               └──────────────────┘              └─────────────┘
       │                                │
       └────────── smith run ──────────┘
              sh -c steps, in graph order
```

1. `smith init` seeds `.smith/repo.yml`; you declare *project roots*.
2. `smith new python/lib --version 1` scaffolds a project from a cookiecutter
   template and pins its type.
3. `smith sync` fetches pinned types (packed *ingots* with hash sidecars)
   into `.smith/types/` and records them in a checked-in lock file — a clone
   of your repo builds with zero setup.
4. `smith <project> <target>` runs the type's steps in the project directory,
   project dependencies first. Hash mismatches anywhere in the custody chain
   (sidecar → lock → installed) are loud errors, never silent swaps.

## Status

Pre-1.0 and moving. Every feature described in these docs works today; the
design is being hardened in real monorepos before a 1.0 tag. Expect sharp
edges, and please open issues.

## Documentation

The full user guide lives in [`docs/`](docs) and is built with
[Zensical](https://zensical.org):

```sh
uvx zensical serve     # live preview at http://localhost:8000
uvx zensical build     # static site in site/
```

Start with the [introduction](docs/index.md), or jump straight to
[getting started](docs/getting-started.md).

## Install

```sh
go install github.com/cliwright/smith/cmd/smith@latest
```

Or build from source: clone, then `go build -o bin/smith ./cmd/smith`.
Requires Go 1.25+; [`mise`](https://mise.jdx.dev) is used for development.

## Sibling repos

| Repo | What it is |
|---|---|
| [smith-project-types](https://github.com/cliwright/smith-project-types) | The public type registry (python, docker, tofu types + schemas) |
| [smith-project-templates](https://github.com/cliwright/smith-project-templates) | Cookiecutter templates used by `smith new` |

## License

[Apache-2.0](LICENSE)

# Core concepts

## Repository

A Smith repository is any directory that contains `.smith/repo.yml`. Smith
finds the repo root by walking up from where you run it, so you can call
`smith` from any directory inside the repo. The config declares the
[project roots](discovery.md), the [registries](registries.md), the
[repo targets](#repo-targets-attachments), and repo-wide settings such as
`tools` and `dotenv`.

## Projects and the manifest

A project is a directory with a `smith.yml`. Smith reads the name and type
from the manifest, not from the directory name, and project names must be
unique across the repo. The full field reference is in
[The smith.yml manifest](smith-yml.md); the short version:

```yaml
version: 1
name: payments
type: python/astral/lib@1
depends_on: [auth]
params:
  python_version: "3.13"
```

The manifest declares what the project is. It never says how to build it —
the type owns the commands.

## Namespaces

A namespace is the first path segment of a project root. `libs/auth` lives
in namespace `libs`. Namespaces give you addressing (`smith libs auth
test`) and grouping (`smith libs test` runs `test` on every project in
`libs` that has one), but they carry no behavior. Two namespaced names are
always unique, so most of the time you can drop the namespace: `smith auth
test` works because project names are unique repo-wide. See
[Running targets](running-targets.md).

## Project types

A type is a versioned bundle of targets, named `language/flavor/kind`:

- `language` — `python`, `docker`, `tofu`, …
- `flavor` — the toolchain variant. Use `std` when the language has one
  standard toolchain; otherwise name the variant (`astral`, `bake`).
- `kind` — what the project is: `lib`, `service`, `image`, `module`, …

`python/astral/lib@1` reads as "a Python library built with the Astral
toolchain, version 1." Types compose a project from several types when
their targets are disjoint; alternatives (bake vs. compose) stay separate
flavors. See [Project types](project-types.md).

## Targets

A target is a named list of shell commands. Types define them; you run
them. Names match `^[a-z][a-z0-9-]*$` with one optional dot segment, so
tooling types can group related targets: `env.get`, `env.put`. A target may
carry a one-line description, which shows up in `smith <name> list`.

## Dependencies

Two mechanisms feed one graph:

- **Type-level `depends_on`** wires targets inside one type: "our `test`
  runs after our `build`".
- **Manifest `depends_on`** wires projects: `- auth` means auth's `build`
  runs before this project's `build`; the explicit form
  `{project, targets, before}` names any target pair.

When you run `smith shop-api build`, Smith builds the transitive closure —
all dependencies of `build`, in topological order, each node once. Cycles
and references to missing targets are hard errors.

## Registries, locks, and custody

Types and templates come from [registries](registries.md) listed in
`repo.yml`. A registry serves each type as an *ingot* (a packed archive)
plus a `.sha256` sidecar. Custody runs in three steps, and each step checks
the previous one:

1. **Sidecar.** The publisher hashes the ingot.
2. **Lock.** `smith sync` verifies the fetched ingot against the sidecar
   and pins its hash in `.smith/lock.json`.
3. **Installed copy.** On every run, Smith checks that the ingot in
   `.smith/types/` matches the lock and that the unpacked files match the
   ingot.

Any mismatch is a loud error that points at `smith sync`. Smith never runs
a type that has changed behind your back.

## Repo targets (attachments)

Some jobs belong to the repo, not to a project: lint everything, roll
credentials, tear down a dev stack. `repo_targets` in `repo.yml` bind an
alias to a `repo/<flavor>/<kind>` type, and Smith runs it against the repo
root as a synthetic project:

```yaml
repo_targets:
  python:
    type: repo/uv/workspace@1
```

```sh
smith repo python lint
```

Attachments never join a bare `smith <target>` run; you address them
explicitly under the reserved `repo` namespace.

## Services

A service is a named set of projects with one command for the whole set.
You define it in a `services/*.yml` file:

```yaml
version: 1
name: shop
components:
  - project: shop-api
  - project: payments
```

Service targets come from a closed vocabulary — `build`, `test`, `lint`,
`push`, `plan`, `deploy`, `destroy`. `smith services shop deploy` runs each
component's `deploy` (or the target it maps to `deploy`), ordering by the
projects' own `depends_on`. Components without the target are skipped and
reported. `smith services shop graph` shows the component graph;
`graph deploy` previews the execution order without running anything.

# The smith.yml manifest

Every project has a `smith.yml`. It declares what the project is: its
name, its type or types, its dependencies on other projects, and its
values for the type's parameters. It never contains build commands. The
type owns the commands; the manifest supplies the data.

```yaml
version: 1
name: payments
type: python/astral/lib@1

depends_on:
  - auth

params:
  python_version: "3.13"

environment:
  PACKAGE: payments
```

## Fields

| Field | Required | What it does |
|---|---|---|
| `version` | yes | Must be `1`. |
| `name` | yes | The project name. Unique across the repo. Matches `^[a-z][a-z0-9-]*$`. |
| `type` | yes | One type ref (`python/astral/lib@1`) or a list of refs for a multi-type project. |
| `depends_on` | no | Projects that must build before this one. Sugar string or explicit mapping (below). |
| `params` | no | Values for the type's parameters. Keys must exist on the type. |
| `environment` | no | Extra environment variables for this project's steps. Values are templates. |
| `dotenv` | no | `true` loads `dotenv/.env` and the selected overlay for this project. Off by default. |
| `working_dir` | no | `project` (default) or `repo`: where steps run. Overrides the type's setting. |

## The type ref

`language/flavor/kind@version`, version as a plain integer: `python/astral/lib@1`.
Refs use `@1`; on disk the installed directory uses `@v1`
(`.smith/types/python/astral/lib@v1/`).

## Dependencies

The sugar string names a project and means "its `build` runs before my
`build`":

```yaml
depends_on:
  - auth            # auth:build before this:build
```

The explicit form names any target pair:

```yaml
depends_on:
  - project: auth
    targets: [build, lint]   # run on auth …
    before: [test]           # … before this project's test
```

Both lists default to `[build]`. Dependency names must match discovered
projects; self-dependencies and dangling names fail discovery.

## Parameters

Types declare parameters with defaults; a manifest may override a value
but cannot declare a new key — an unknown key is a hard error that names
the project, the key, and the type that should own it. Values are
strings, rendered as templates against the `SMITH_*` variables, and a
param value cannot reference another param.

```yaml
# type declares: params: {python_version: {default: "3.12"}}
params:
  python_version: "3.13"     # this project builds on 3.13
```

Parameters are the type-sanctioned way to vary behavior between projects
that share a type. If two projects need structurally different builds,
that is a signal for a new type flavor, not a new parameter.

## Environment

`environment` sets variables for this project's steps. Values are Go
templates over the merged parameters and `SMITH_*` variables, so
`PACKAGE: payments` and `WHEEL: "{{.PACKAGE}}-{{.python_version}}"` both
work. Manifest values beat type values; see
[Environments](environments.md) for the full precedence chain.

## dotenv

`dotenv: true` opts this project into the conventional `dotenv/`
directory next to the manifest. See
[Environments](environments.md#the-env-integration-dotenv).

## working_dir

Type steps run in the project directory by default. Set `working_dir: repo` to run them from the repo root instead — for example, an image project
whose build context includes sibling directories. The manifest's setting
wins over the type's.

## Multi-type projects

`type` also accepts a list. The types compose the project, and they must
be disjoint: no shared target names, parameter keys, or environment keys,
and they must agree on `working_dir`. A type's `depends_on` can only wire
its own targets; cross-type wiring is a hard error. Use manifest
`depends_on` to order across projects.

```yaml
type:
  - python/astral/lib@1
  - repo/attic/docs@1
```

`SMITH_TYPE_DIR` then points at the owning type's installed directory for
whatever target is running. Smith writes a one-element list back as the
plain string form.

## What smith.yml will never do

There are no per-project target overrides and no inline command hooks.
Build logic belongs to versioned types; wiring belongs to manifests. When
you need new behavior, you change the type (and bump its version), so
every project picks it up through the same review and sync flow.

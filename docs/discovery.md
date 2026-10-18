# Project discovery

## Finding the repo

Smith finds your repository by walking up from your current directory
until it finds `.smith/repo.yml`. Run `smith` from anywhere inside the
repo; it always acts on the whole repository. Outside a repo, commands that
need one tell you to run `smith init`.

## Declared roots only

Smith never scans your repository on its own. It traverses only the roots
you declare in `.smith/repo.yml`:

```yaml
workspace:
  project_roots:
    - libs
    - tools
    - services
    - images
```

`smith init` seeds the conventional set. `smith new` appends a root when
it scaffolds a project outside the declared ones. A listed root that does
not exist is skipped, so you can share one config across repos with
different layouts.

While walking a root, Smith skips the usual build and cache directories:
`.git`, `node_modules`, `.venv`, `venv`, `__pycache__`, `.tox`,
`.mypy_cache`, `.pytest_cache`, `.ruff_cache`, `dist`, and `target`. This
is what keeps discovery fast and predictable.

You can also declare `.` as a root. Then any `smith.yml` at the repo root
is a project like any other.

## What counts as a project

Any directory under a declared root that contains a `smith.yml` is a
project. Smith reads the project's name from the manifest, not from the
directory name, and names must be unique repo-wide — two projects with the
same name stop discovery with an error that names both directories. The
same check rejects dependency entries that point at a missing project, or
at the project itself.

Dependencies are validated during discovery, so `smith tree` is a quick
way to audit a repo's wiring.

## Namespaces come from the roots

A namespace is the first path segment of a project's path. A root `libs`
gives you namespace `libs`; a project at `libs/auth` is in it. A nested
root like `libs/teams` joins the same namespace: its projects live at
`libs/teams/<name>` and answer to `libs`. Several roots can share one
namespace.

Because names are unique repo-wide, namespaces are a convenience for
grouping and addressing, not a requirement. See
[Running targets](running-targets.md) for how addressing resolves.

One special case: a root literally named `services` does double duty.
Directories under it are ordinary projects, and any `*.yml` file directly
inside it is a service definition (a named composition of projects; see
[Core concepts](concepts.md#services)). Likewise, the names `repo` and
`services` are reserved as command keywords and always route there.

## Seeing what Smith found

```sh
smith list          # the declared roots, plus the reserved "repo"
smith tree          # roots, projects, and each project's type
```

```text
$ smith tree
smith-shop
├── libs
│   ├── auth          (python/astral/lib@1 -> payments)
│   └── payments      (python/astral/lib@1)
├── images
│   └── api-image     (docker/bake/image@1)
```

The annotation after each project is its type ref (comma-joined when a
project has several types), followed by `->` and its project dependencies
when it has any.

```sh
smith tree --show-deps
```

adds a `Dependencies:` section with one line per manifest `depends_on`
entry, showing exactly which targets run before which.

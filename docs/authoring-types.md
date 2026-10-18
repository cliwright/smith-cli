# Author a project type

A project type is the encoding of "how we build X". This page walks from
an empty directory to a published type. The field reference is
[Project types](project-types.md); here we cover the workflow.

## 1. Scaffold

```sh
smith new smith/type --version 1
```

Answer the prompts for language, flavor, kind, and version. Smith renders
the `smith/type` template into the `types/` root as a registry-layout tree
(`types/python/acme/lib/v1/…`) and checks that the scaffolded
`project-type.json` agrees with where it landed. `types/` is added to
`workspace.project_roots` if needed.

You can also create the tree by hand — a type is just a directory that
looks like this:

```text
types/python/acme/lib/v1/
├── project-type.json
├── scripts/
│   └── publish.sh          # optional
└── help/
    ├── project.md          # optional
    └── build.md            # optional
```

## 2. Write the definition

Edit `project-type.json`. Keep these rules in mind; Smith enforces all of
them at load, and `smelt` enforces the position rules:

- `name` must equal the last three path segments
  (`python/acme/lib`), and `version` must equal the `v<N>` directory.
- `tools` must list every executable your steps call. This is the BYOT
  contract; `smith doctor` checks it.
- Every parameter needs a `default`. Params are string-only and lowercase,
  so they can never collide with `SMITH_*` variables.
- Steps and environment values are `{{.templates}}` with
  `missingkey=error`. Smith renders the whole type at load, so a typo
  fails loudly at `sync` time, not mid-build.
- Put long commands in `scripts/` and call them as
  `{{.SMITH_TYPE_DIR}}/scripts/publish.sh`. Scripts ride the ingot, so the
  type stays self-contained — never reference another type's files.

A type's `depends_on` may only wire its own targets. Cross-project wiring
belongs in manifests.

## 3. Test locally with a `path` registry

You do not need to publish anything to try a type. In the repo that should
consume it, add a local registry and list it first:

```yaml
registries:
  - name: dev
    types: { path: types }               # your in-repo tree, read in place
  - name: cliwright
    types:     { git: https://github.com/cliwright/smith-project-types }
    templates: { git: https://github.com/cliwright/smith-project-templates }
```

Create a test project with a manifest that refs your type, then:

```sh
smith sync        # installs your type straight off disk
smith <project> build
smith <project> build --env dev
```

Because local sources and git sources share the fallback logic, an
unpublished type syncs off disk while everything else still comes from
GitHub.

## 4. Pack with `smelt`

```sh
smith smelt --path types/python/acme/lib/v1
```

`smelt` checks the registry invariants (directory name ↔ file name,
`v<N>` ↔ `"version"`), packs the directory into
`python-acme-lib@v1.ingot` (everything except sidecars and old artifacts),
and writes the `.sha256` sidecar. Re-run it any time; it overwrites.

## 5. Publish

Copy the version directory — `project-type.json`, its sidecar, the ingot,
its sidecar — into your registry repo, commit, push. That is the whole
release. Consumers pick the type up with `smith sync`; the lock pins the
hash the first time it installs.

Published versions do not change. Fix a bug by copying the tree to `v2`,
bumping `"version": 2`, and smelting again. Consumers upgrade by editing
their `smith.yml` refs when they are ready — the old version keeps working
for everyone else.

## Iterate

The loop is short: edit, `smelt --path …`, `smith sync` in the consumer,
run. When the type is stable, move the version directory to the shared
registry repo and delete the local one.

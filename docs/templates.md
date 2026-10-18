# Project templates

Templates scaffold new projects. `smith new` renders a
[cookiecutter](https://cookiecutter.readthedocs.io/) template, and the
rendered project lands in your repo with a ready `smith.yml`. Templates
answer the question "what files does a project of this kind start with?";
types answer "how do I build it?". The two pair up, but they live in
separate registries.

## Creating a project

```sh
smith new python/lib --version 1
```

- `<language>/<kind>` picks the template: `python/lib`, `python/service`,
  `docker/image`, `tofu/infra`, and so on. Kinds map to project roots:
  `lib` → `libs`, `service` → `services`, `image` → `images`,
  `infra` → `infra`, `tool` → `tools`.
- `--version` is required and picks the template version (`v1`, `v2`, …).
  Templates are versioned exactly like types.
- Smith runs `cookiecutter`, asks the template's questions, and writes the
  project into the kind's root. If that root is not in
  `workspace.project_roots` yet, Smith appends it, preserving your
  comments.

The template ships the project's `smith.yml` — name, type ref, and
sensible defaults. Edit it after scaffolding.

To scaffold a project type itself, use the pseudo-language `smith`:

```sh
smith new smith/type --version 1
```

This renders a type directory in registry layout under the `types/` root,
checks that the scaffolded `project-type.json` agrees with its position,
and prints the next step: `smith smelt --path …`. See
[Author a project type](authoring-types.md).

## The template registry

`smith init` seeds one registry:

```yaml
registries:
  - name: cliwright
    types:     { git: https://github.com/cliwright/smith-project-types }
    templates: { git: https://github.com/cliwright/smith-project-templates }
```

`git` sources are GitHub repos. Smith hands the URL to `cookiecutter`,
which reads the template straight from the repo — no clone step for you to
manage. Template paths inside a templates repo are two-part,
`<language>/<kind>/v<N>`; the flavor never appears in the path, because
variation between flavors lives inside the template (cookiecutter choices
plus a post-gen hook that prunes the files you did not pick — the
`docker/image` template uses this for its bake-vs-compose choice).

## Private and local templates

A registry may use `path` instead of `git` for its templates:

```yaml
registries:
  - name: local
    types:     { path: types }
    templates: { path: /absolute/path/to/smith-project-templates }
    # relative paths also work when you run smith from the repo root
```

This is how you develop templates before publishing them, or how you keep
templates private: point `templates.path` at a local checkout or an
internal mirror and list it before the public registry.

One current limitation, stated plainly: `smith new` tries the **first**
registry that has a `templates` source and does not fall back to the next
one if the template is missing there. While a template is in development,
put your local registry first. (`smith sync` does fall back across
registries; `smith new` does not — yet.)

## The contract for template authors

A template is a cookiecutter directory at `<language>/<kind>/v<N>`. Its
only hard contract with Smith: rendering it must produce a directory
containing a valid `smith.yml`. Everything else — source layout, tests,
CI files — is yours. See
[Author a template](authoring-templates.md).

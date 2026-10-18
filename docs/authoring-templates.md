# Author a project template

A template is a cookiecutter directory that Smith can render into a new
project. This page covers the parts of the contract that involve Smith;
everything else is standard cookiecutter.

## Where templates live

In a templates repo, templates sit at `<language>/<kind>/v<N>`:

```text
smith-project-templates/
├── python/lib/v1/
│   ├── cookiecutter.json
│   ├── {{cookiecutter.pypi_package_name}}/
│   │   ├── smith.yml
│   │   ├── src/ …
│   │   └── tests/ …
│   └── hooks/post_gen_project.py     # optional
├── docker/image/v1/
└── smith/type/v1/
```

The `v<N>` directory is the version `smith new --version N` selects.

## The contract with Smith

1. **Rendering must produce a directory with a `smith.yml`.** Smith checks
   nothing else; the manifest inside the template should carry a valid
   `version`, `name` (typically `{{cookiecutter.project_slug}}`), and
   `type` ref. Keep the name pattern `^[a-z][a-z0-9-]*$` — underscores in
   slugs will not validate.
2. **Type refs in the shipped `smith.yml` use `@N`** (`python/astral/lib@1`),
   while the type's registry path is `…/v1/`. Cookiecutter's `{% raw %}`
   escaping keeps Go template expressions (`{{.param}}`) intact through
   rendering when you need them in generated files.
3. **Flavor variation lives inside the template, not in the path.** A
   `docker/image` template can ask "bake or compose?" in
   `cookiecutter.json` and prune or rename files in a post-gen hook for
   the choice not taken. The template path never carries the flavor.

Smith runs cookiecutter for you:

```text
cookiecutter <source> --directory <language>/<kind>/v<N> --output-dir <repo root>
```

`cookiecutter` must be on `PATH` (BYOT; `smith doctor` checks it), and it
runs interactively, so template questions are answered in your terminal.

## Test locally

Point a registry's `templates` source at your local checkout and list it
first:

```yaml
registries:
  - name: dev
    templates: { path: /absolute/path/to/smith-project-templates }
  - name: cliwright
    templates: { git: https://github.com/cliwright/smith-project-templates }
    types:     { git: https://github.com/cliwright/smith-project-types }
```

```sh
smith new python/lib --version 1
```

One caveat to know while testing: `smith new` uses the first registry that
has a `templates` source and does not try the next one when the template
is missing there. Keep your dev registry first until the template is
published.

`templates.path` is passed to cookiecutter as written. Absolute paths
always work; relative paths work when you run `smith` from the repo root.

## Publish

Push the version directory to the templates repo. `smith new` reads
templates straight from the git source, so there is no pack or smelt step
— the directory itself is the artifact.

# Authoring project templates

`smith new` scaffolds projects from [cookiecutter](https://cookiecutter.readthedocs.io/)
templates living in a templates registry. Templates are how "what a project
looks like on day one" is shared — the rendered project comes with a
hand-editable `smith.yml` already pointing at the right type.

## Layout

Templates are addressed **two-part** — `<language>/<kind>/v<N>` (the flavor
never appears in the template path; variation between flavors lives *inside*
the template):

```
python/
└── lib/
    └── v1/
        ├── cookiecutter.json
        ├── hooks/
        │   └── post_gen_project.py    # optional: prune/rename per choices
        └── {{ cookiecutter.project_name }}/
            ├── pyproject.toml
            ├── README.md
            ├── src/…
            └── smith.yml              # rendered, then hand-edited
```

The rendered `smith.yml` is a starting point — Smith deliberately does not
guess project names, types, or params for you beyond the template's choices.

```yaml
version: 1
name: {{ cookiecutter.project_name }}
type: python/astral/lib@1      # rendered; hand-edit before first sync
```

## Choices and post-gen hooks

Variation inside a template is ordinary cookiecutter: `cookiecutter.json`
choices plus a `post_gen_project.py` hook that prunes or renames files based
on the answers. The `tofu/infra/v1` template uses this for the cloud
provider — one template renders AWS-, GCP-, or Azure-shaped projects, and the
hook generates the per-provider `environments/*.tfvars` files:

```json
{
  "project_name": "my-infra",
  "cloud_provider": ["aws", "gcp", "azure"]
}
```

## Adding a template

1. Create the tree under `<language>/<kind>/v<N>/` in the templates repo.
2. Iterate locally: point your repo's `.smith/repo.yml` at a local clone —
   `smith new` honors a `templates.path` source (the dev escape hatch):

   ```yaml
   registries:
     - name: local
       templates:
         path: ../smith-project-templates
   ```

3. Render-check with `smith new <language>/<kind> --version N` in a scratch
   repo. (Known limitation: `smith new` tries only the **first** templates
   source today — keep your local source first while developing.)

## Relationship to types

Templates and types are paired but independent: the template's rendered
`smith.yml` references a type ref (`tofu/std-whoiam/infra@1`), and `smith
sync` fetches it from the **types** registry. Template paths need not mirror
the three-part type names they pair with — `tofu/infra/v1` (template)
renders projects for `tofu/std-whoiam/infra@1` (type).

Rule of thumb: the **template** decides files and defaults (structure); the
**type** decides commands (behavior). When behavior diverges structurally
(bake vs. compose), that's a new type flavor — not a new template knob.

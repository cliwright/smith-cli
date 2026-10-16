# Passing arguments (`--`)

Tools want flags. Smith targets are fixed command strings — so every run
accepts **everything after `--` verbatim** and hands it to the steps as
`SMITH_CLI_ARGS`: both a template variable `{{.SMITH_CLI_ARGS}}` and an
environment variable `$SMITH_CLI_ARGS` (empty string when nothing was
passed).

```sh
smith spam plan -- -var-file=custom.tfvars
smith spam apply -- -auto-approve
smith spam state -- list
```

`smith spam plan -- -var-file=custom.tfvars` renders a step

```json
"tofu plan {{if .SMITH_CLI_ARGS}}{{.SMITH_CLI_ARGS}}{{else}}--var-file=environments/$SMITH_ENV.tfvars{{end}}"
```

as `tofu plan -var-file=custom.tfvars` — and with no `--`, as the default
`tofu plan --var-file=environments/dev.tfvars`. That replace-the-default
pattern is the idiomatic way to give users an escape hatch while keeping the
everyday command automatic.

## Notes

- `--` is captured at the top level and broadcast through the whole run: in a
  dependency-graph run, every project's steps see the same value.
- Quoting is preserved: tokens are re-joined shell-safely, so
  `-- -var 'tag=a b'` arrives as two arguments, not three.
- `--` before `--help` still gets you target help — flags after `--` are
  never intercepted by Smith itself.
- `SMITH_CLI_ARGS` is **not** part of the param namespace; it's a computed
  var like `SMITH_REPO_ROOT`/`SMITH_PROJECT`. Lowercase param keys and
  uppercase `SMITH_*` names can never collide.

## When a type doesn't use it

Types that never reference `SMITH_CLI_ARGS` simply ignore what you pass —
nothing breaks. `--` is always safe, and only as useful as the type makes it.

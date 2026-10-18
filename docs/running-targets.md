# Running targets

## The grammar

Every run command follows one shape:

```text
smith <namespace> <project> <target>
```

Project names are unique across the repo, so a uniquely named project does
not need its namespace:

```sh
smith libs auth test    # namespaced
smith auth test         # same thing
```

Useful forms at a glance:

| Command | What it does |
|---|---|
| `smith` | Prints help |
| `smith list` | Lists namespaces (the project roots) plus `repo` |
| `smith <ns>` | Lists projects in the namespace |
| `smith <name>` | Lists one project's targets |
| `smith <name> list` | Same, explicit |
| `smith <name> help` | Help for the project and its type |
| `smith <name> <target> help` | Help for one target |
| `smith <ns> <target>` | Runs the target on every project in the namespace that has it |
| `smith <target>` | Runs the target repo-wide |
| `smith repo <alias> <target>` | Runs an attachment (a repo-level target) |
| `smith services <name> <target>` | Runs a service target across its components |

Target names may carry one dot segment (`env.get`, `env.put`), so tooling
types can group related commands: `smith tooling env.get`.

## How Smith reads your words

When the first word after `smith` is not a command or a reserved keyword,
Smith resolves it like this:

- **Two or more words:** an exact project name wins. `smith libs build`
  runs project `libs`'s `build` target if a project named `libs` exists;
  otherwise `libs` reads as a namespace.
- **One word:** namespace first, then a repo-wide target, then a project
  name. `smith build` never gets shadowed by a project named `build`; it
  still runs `build` everywhere.
- **Reserved words:** `repo` and `services` route to their own grammars
  and always win. `list` and `help` are reserved in grammar positions.
  Command names (`sync`, `new`, `tree`, …) win over same-named projects.

Exact matches only; there is no fuzzy guessing. When Smith cannot resolve
your words, the error tells you what it knows: unknown targets list the
available ones, unknown namespaces list their projects, and a dead end
suggests `smith list`.

## What a run looks like

```text
$ smith shop-api build
==> payments:build
$ uv build
...
==> shop-api:build
$ uv build
...
```

Smith builds a plan before it runs anything:

1. Start from every (project, target) pair you addressed.
2. Add every dependency: the type's own `depends_on` wiring plus the
   manifest's project dependencies, transitively.
3. Sort the graph topologically; each node runs once.
4. Run the steps of each node in order.

Runs are serial and fail-fast: the first failing step stops the run and
reports `<project>: <target>: sh: exited with status N`. Cycles
(`dependency cycle: a:x -> b:y -> a:x`) and references to missing targets
are hard errors before anything executes.

Each step runs with `sh -c`, with your terminal attached, in the project
directory — or the repo root when the type's `working_dir` is `repo` (a
manifest may override this per project).

Namespace-wide, repo-wide, and service runs skip projects that lack the
target and say so at the end:

```text
ran 3, skipped 1 (type has no 'lint' target)
```

## Passing arguments: `--`

Everything after a standalone `--` is captured verbatim and handed to the
steps as one shell-quoted string, `$SMITH_CLI_ARGS`:

```sh
smith payments test -- -k refunds -x
```

Inside a step, `$SMITH_CLI_ARGS` expands to `-k refunds -x`. The value is
also available as the template variable `{{.SMITH_CLI_ARGS}}`, always
defined (empty when you pass nothing). Smith quotes the tokens so they
survive the trip through `sh -c` intact. Use it to forward flags to the
underlying tool without adding parameters to the type.

## Flags on run commands

- `--env <name>` — select an [environment](environments.md). Overrides
  `$SMITH_ENV` and the repo default.
- `--context` — do not run; print the resolved environment for the
  addressed project or target instead. Shares the run's rules and errors.
- `--parallel N` — accepted for future use; today Smith warns and runs
  serially.

## Help without leaving the terminal

```sh
smith payments help        # the type: variables with your values, env, docs
smith payments test help   # one target: rendered steps, vars used, docs
```

Help output is markdown. Smith renders it with `glow` when `glow` is on
your `PATH` (raw markdown otherwise) and pages it through `$SMITH_PAGER`,
then `less -R`, then `more`, then plain stdout. The generated summary is
built from your actual configuration; types can add their own pages under
`help/` in the type directory.

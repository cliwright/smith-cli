# Running targets

## The grammar

```sh
smith <project> <target>          # the everyday form (crispy)
smith <ns> <project> <target>     # namespaced form — always works
smith <ns> list                   # projects in a namespace
smith <project> list              # targets of one project
```

Smith classifies each argument position for you. `smith payments test` and
`smith libs payments test` are the same command; the namespace is optional
whenever the project name is unambiguous. With one argument, Smith tries, in
order: a namespace (`smith libs`) → a repo-wide target run across all
projects that implement it (`smith build` — see below) → a bare project
listing.

Every project command also takes the run flags: `--env NAME` (environment
selector), `--context` (print the resolved environment instead of running),
and `--` (extra args for the tool — see [Passing arguments](cli-args.md)).

## Dependencies come along

`smith api build` doesn't just build `api`. The run closure includes:

1. **Type-level deps** — the type's `depends_on` (`build: [lint, test]`).
2. **Project-level deps** — `depends_on` in each manifest (`auth` before
   `api`'s build, …).

Smith merges everything into a graph of `(project, target)` nodes, orders it
topologically, runs it serially, and fails fast. Cycles and dangling targets
are hard errors, not warnings. Projects that don't implement the requested
target are skipped and reported — unless they're pulled in as prerequisites.

A bare `smith build` runs the `build` target repo-wide: every project whose
type implements `build`, with all cross-project dependencies honored. (Repo
attachments never join bare runs.)

## Listing what exists

```sh
smith list                 # namespaces + the reserved `repo`
smith tree                 # discovered projects and their type refs
smith tree --show-deps     # …plus the dependency edges
smith <project> list       # targets, annotated with descriptions
```

For a multi-type project, `list` groups targets under per-type headings —
you can always see which type owns which verb.

## `list`, `help`, and `doctor`

- `smith <project> list` — targets and one-line descriptions.
- `smith <project> help` / `smith <project> <target> help` — generated docs
  (resolved vars, rendered steps) composed with the type's shipped
  `help/*.md` markdown, piped through `glow` (if installed) and a pager
  (`$SMITH_PAGER` > less/more > stdout).
- `smith doctor` — BYOT health check: every tool declared anywhere in the
  repo, probed with `--version`; non-zero exit when unhealthy.
- `smith types` — browse the repo's installed types: what's pinned, what
  mismatches, full detail per type.

## Multi-type projects

```yaml
# libs/client/smith.yml
type:
  - python/astral/lib@1     # build, test, lint, …
  - acme/sso/headers@1      # headers.check, headers.fix
```

```sh
smith client headers.check
```

Targets from every listed type are available on the project. Name collisions
across types fail loudly at load — that's the design: verbs are a shared
namespace, so compatible types are built to complement, not collide. Tooling
types conventionally namespace with one dot segment (`env.get`, `state.list`).

## Services and attachments

Service runs follow the same grammar under the reserved plural namespace:

```sh
smith services                 # list service compositions
smith services shop            # show the component set
smith services shop build      # run a service target across components
smith services shop graph      # component dependency view (project-level)
smith services shop graph build  # execution-order preview; nothing runs
```

Repo attachments under `repo`:

```sh
smith repo python sync         # run a repo-level target
smith repo python list         # an attachment's targets
```

## Failure behavior

Steps run in order; the first non-zero exit fails the target and the run.
`--env`-required types should fail early and loudly — the idiomatic guard is
`${SMITH_ENV:?…}` in the step — so mistakes surface before your tool runs,
not inside it.

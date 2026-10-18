# Registries and ingots

Types and templates come from registries. A registry is a named entry in
`.smith/repo.yml` with a `types` source, a `templates` source, or both:

```yaml
registries:
  - name: cliwright
    types:     { git: https://github.com/cliwright/smith-project-types }
    templates: { git: https://github.com/cliwright/smith-project-templates }
  - name: local
    types:     { path: types }        # a registry tree inside this repo
```

Order is priority. When Smith looks for a type, it asks each registry in
list order and stops at the first one that has it.

## Two kinds of source

**`git` — a GitHub repository.** Smith fetches over plain HTTPS from the
repo's default branch: no clone, no GitHub API, no commit pinning. For
each type it makes exactly two requests — the packed artifact and its hash
sidecar. Anything that is not a `github.com` URL is rejected today.

**`path` — a local directory.** The directory must already be in registry
layout (what `smith smelt` produces). Smith reads it in place; relative
paths resolve against the repo root. A missing file is the local
equivalent of a 404: Smith moves on to the next registry.

If no registry has the type, the run stops with a clear error. Smith
never guesses and never falls back to an unofficial source.

## Registry layout

A registry is a directory tree:

```text
python/astral/lib/v1/
├── project-type.json          # the type definition
├── project-type.json.sha256   # the publisher's hash of the definition
├── python-astral-lib@v1.ingot        # the packed artifact
└── python-astral-lib@v1.ingot.sha256 # the publisher's hash of the artifact
```

The version lives in the path (`v1`) and in the file (`"version": 1`), and
the two must agree — `smith smelt` enforces this. Types are immutable by
policy: a published version never changes. A change ships as a new `v2`
directory.

## The ingot

An *ingot* is the packed form of a type: a tar archive of the type
directory — `project-type.json`, plus `scripts/` and `help/` when present.
Sidecars and older artifacts are never packed. The archive is
reproducible: packing the same tree twice gives byte-identical output, so
its hash is stable. The file name flattens slashes to dashes:
`python-astral-lib@v1.ingot`.

The ingot is the unit of distribution, installation, and verification.
`project-type.json` is the unit you author and read; the ingot is how it
travels.

## Chain of custody

Smith tracks every type from publisher to run in three steps, and checks
each link:

1. **Sidecar.** The publisher runs `smith smelt`, which writes the ingot
   and its `.sha256` sidecar. `smelt` refuses a tree whose path and file
   disagree about the type's name or version.
2. **Lock.** `smith sync` fetches ingot + sidecar, verifies the hash,
   installs the type into `.smith/types/python/astral/lib@v1/` (artifact
   plus unpacked files), and pins the ingot's hash in
   `.smith/lock.json`:

   ```json
   {
     "version": 1,
     "types": {
       "python/astral/lib@1": {
         "registry": "cliwright",
         "hash": "sha256:…"
       }
     }
   }
   ```

3. **Load.** Every run re-verifies the installed copy: exactly one ingot,
   its hash matches the lock, and every unpacked file matches the ingot
   byte for byte.

A broken link is a loud error that names the type and points at
`smith sync`. Smith never runs a type that has drifted.

## What `smith sync` does

```sh
smith sync                  # fetch every type this repo uses
smith sync tofu/aws-trivy/module@1   # … or only these, named explicitly
smith sync --force          # wipe .smith/types and the lock, rebuild
```

- With no arguments, the fetch set is the union of every project's type
  refs and every attachment's type. With arguments, only those. Already-installed, still-verifying types are skipped
  (`up to date`).
- Fetches stage in memory and commit only if all of them succeed.
- After a successful sync, Smith unions the fetched types' `tools` into
  `tools:` in `repo.yml`, then writes the lock atomically.
- `--force` deletes the lock and everything under `.smith/types/` first —
  the clean slate after registry changes or a broken cache.

`.smith/lock.json` and `.smith/types/` are meant to be checked in: a fresh
clone builds with zero setup. If you prefer a per-repo cache, gitignore
`.smith/types/` instead; Smith reads either and `sync` rebuilds it.

## Private and local registries

Nothing about a registry requires the public repos. A `git` source can be
your organization's GitHub repo; a `path` source can be any directory on
disk. List your private registry first and public ones as fallback, or
keep everything local.

The `path` source doubles as the development loop for type authors: run
`smith new smith/type` to scaffold, `smith smelt --path …` to pack, then
point a `types.path` registry at the tree and sync straight off disk. The
same repo consumes the type before anything is pushed anywhere. See
[Author a project type](authoring-types.md).

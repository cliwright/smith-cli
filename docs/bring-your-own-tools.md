# Bring your own tools

Smith installs nothing. Every step runs as a plain shell command, so every
tool a type needs must already be on your `PATH`. Smith calls this BYOT:
bring your own tools.

## Why this is simple

Each language already ships a toolchain that its users know. Python has
`uv` or `pip`. Rust has `cargo`. Go has `go`. Containers have `docker`;
OpenTofu has `tofu`. Smith builds on that instead of competing with it:

- **No plugins, no SDK.** A type is a JSON file with shell commands. If you
  can run it in a terminal, Smith can run it.
- **Your versions, your schedule.** Upgrade `uv` when you are ready. Smith
  never pins or vendors a tool for you.
- **One type per toolchain.** "How we build Python libraries" becomes a
  versioned artifact the whole org shares. New language? Write a new type.
  Smith itself does not change.
- **No hidden state.** Steps run on your machine, in your project
  directory, with your environment. What you see is what runs.

A type declares the executables it needs in its `tools` list:

```json
{
  "name": "python/astral/lib",
  "version": 1,
  "tools": ["uv"]
}
```

## Check your toolbox: `smith doctor`

`smith doctor` collects the tools your repo needs — the union of the
repo-level `tools` list and every type's `tools` across all projects and
attachments — and probes each one on `PATH`:

```text
$ smith doctor
uv       0.9.5   needed by: repo, payments, shop-api
docker   28.1.1  needed by: api-image
cookiecutter 2.6.0  needed by: repo

missing:
tofu     needed by: network

doctor: 3 ok, 1 missing
```

Each line shows the tool, the version it reported (or `version unknown`),
and who needs it. Missing tools are listed under `missing:` and `doctor`
exits non-zero, so you can gate CI on it.

`smith new` also follows BYOT: it calls `cookiecutter`, so `cookiecutter`
must be on `PATH`.

## The trade-off

BYOT means setup is your job. A fresh machine needs the tools your types
use before `smith build` will pass. The payoff: Smith stays small, stays
out of your toolchain's way, and never fights your OS package manager,
version manager, or container runtime.

# Config from environment (0002)

**Status:** Accepted, 2026-06-06

## Context

`shortn` runs in containers and in Kubernetes. In those environments
configuration is injected at runtime — by `docker run -e`, a compose
`environment:` block, a k8s `ConfigMap` or `Secret`. The same image runs
unchanged in dev, CI, and prod with only the environment differing. This is the
config factor of the [12-factor app](https://12factor.net/config) methodology:
strict separation of config from code.

Concrete forces:

- No rebuilds to reconfigure. Changing a port or log level must not require a
  recompile or a new image.
- One central place. Config reads scattered across the codebase rot. A single
  struct and a single `Load()` mean "what knobs exist?" has one answer.
- It must grow cleanly. Adding a field should be a one-line change in one file.
- Secrets stay out of the repo. No connection strings or tokens hardcoded or
  committed.

## Decision

Configuration loads from environment variables using only the standard library,
behind a small package `internal/config`:

- a `Config` struct whose fields are the typed knobs the app needs;
- a `Load()` func that reads the environment, applies fallbacks, and returns a
  populated `Config` (and an error if a required value is missing/invalid);
- a private `getEnv(key, fallback string) string` helper for "read with default".

`Load()` is called once in `cmd/api/main.go`, and the resulting `Config` is
passed down explicitly — no global state. Each new knob is appended as one struct
field plus one read in `Load()`.

## Alternatives considered

- Hardcoding values — rejected outright. Breaks dev/prod parity, forces a
  rebuild per environment, and invites committing secrets. Non-starter for
  containers.
- Command-line flags (`flag` pkg) — rejected. Flags are awkward to inject in
  k8s manifests and compose files, and env is the 12-factor standard that the
  whole container ecosystem assumes. Flags remain fine for one-off CLI tools.
- `viper` / `envconfig` / similar — rejected for now. They add a dependency and
  surface area (file formats, key casing magic, struct tags) the config doesn't
  need at this size. The stdlib approach is zero-dep and trivially understood. If
  config later sprawls into nested files or many sources, one can be adopted and
  this ADR superseded — the centralized `Load()` makes that swap localized.

## Consequences

**Good**

- Zero dependencies, fully stdlib — nothing to audit or version-bump.
- 12-factor compliant: same image everywhere, config injected at runtime; drops
  straight into compose `environment:` and k8s `ConfigMap`/`Secret`.
- Trivial to extend: each new knob appends one struct field plus one `getEnv` line.
- One source of truth for "what can be configured."

**Bad / trade-offs**

- Manual parsing and validation. Non-string types (ints, durations, bools) must
  be parsed and error-checked by hand in `Load()`; a library would do this via
  struct tags.
- No typed-config niceties: no automatic required-field enforcement, no
  defaults-from-tags, no built-in `.env` file loading. Acceptable while the config
  is small.
- Easy to forget validation. A missing required var should fail fast in `Load()`,
  not surface as a nil/empty value deep in a request handler.

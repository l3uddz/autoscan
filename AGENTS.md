# Agent guidance for autoscan

This file gives repository rules for coding agents. It refines the owner's
global agent policy. If a rule here conflicts with an explicit user
instruction, follow the user instruction.

## Project status

This repository (`l3uddz/autoscan`) is maintained. It continues the original
`Cloudbox/autoscan` project. The original repository is archived and is
end-of-life. Do not copy end-of-life notices from the original into this
repository.

Commit history before PR #1 comes from the original repository. PR numbers in
those commit subjects, for example `#238`, point to `Cloudbox/autoscan`, not
to this repository.

## What autoscan does

Autoscan is a Go service. Triggers receive file change events and turn them
into scans. A scan is a folder path with a priority. The processor stores
scans in SQLite and sends them to targets. Targets are media servers that
start a library scan for the folder.

| Path | Content |
| --- | --- |
| `autoscan.go` | Core types: `Scan`, `Trigger`, `HTTPTrigger`, `Target`, errors, rewriters, filters |
| `cmd/autoscan/` | CLI entry point, configuration, HTTP router, trigger and target wiring |
| `triggers/<name>/` | One package for each trigger (Sonarr, Radarr, Readarr, Lidarr, manual, inotify) |
| `targets/<name>/` | One package for each target (Plex, Emby, Jellyfin, autoscan) |
| `processor/` | Scan queue and SQLite datastore |
| `processor/migrations/` | Embedded SQL migrations for the datastore |
| `migrate/` | Migration runner |
| `docker/Dockerfile` | Container image, built from the goreleaser output in `dist/` |

## Code changes

- If you add a trigger, add a package under `triggers/` and add its
  configuration to `cmd/autoscan/main.go`. Register an `HTTPTrigger` (webhook)
  in `cmd/autoscan/router.go`. Register a background `Trigger`, such as
  inotify, in `cmd/autoscan/main.go`.
- If you add a target, add a package under `targets/`. Register it in
  `cmd/autoscan/main.go`.
- Put webhook test payloads in the `testdata/` directory of the trigger package.
- If you change the datastore schema, add a new numbered file such as
  `2_<name>.sql` to `processor/migrations/`. Do not edit a migration that a
  release already contains.
- If you change user-facing behavior or configuration, update `README.md`.
- The Go version comes from `go.mod`. Do not lower it.

## Checks

You need Go and [Task](https://taskfile.dev). CI runs these commands in this order:

```sh
task vendor
task test
```

`task vendor` creates `vendor/` and runs `go mod tidy`. Git ignores `vendor/`.
Do not commit it. If `go mod tidy` changes `go.mod` or `go.sum`, commit
those changes with the change that caused them.

Before you push, run this command too:

```sh
task lint
```

`task lint` runs `gofmt` on tracked Go files and then `go vet ./...`. If
`gofmt` lists a file, the task fails. CI does not run it. `task build` makes a local
binary in `dist/`.
`task snapshot` needs goreleaser 1.7.0, which CI uses.

## CI side effects

The `Build` workflow in `.github/workflows/build.yml` runs on pushes to
branches with no `/` in the name, on `v*` tags, and on PRs into `master`.
The push filter `"*"` does not match `/`, so a push to `fix/<issue>-<slug>`
runs CI only after you open a PR into `master`.

- If a run is not from a fork, CI publishes a public Docker image tag,
  `l3uddz/autoscan:<branch>`, with slashes replaced by hyphens.
- If you delete a branch, the `Docker Cleanup` workflow removes its Docker tag.
- If someone pushes a `v*` tag, CI publishes a GitHub release and the Docker
  tags `<version>` and `latest`. Do not create or push tags unless the user
  asks for a release.

## Delivery

- The default branch is `master`. It is the only integration branch.
- Start each change on a short-lived branch from `master`, for example
  `fix/<issue>-<slug>` or `docs/<issue>-<slug>`.
- Track work in GitHub issues on this repository. If no issue exists, create
  one before you start, or ask the user. Do not invent an issue number.
- Use Conventional Commits with a lowercase scope, for example
  `fix(plex): handle missing library sections`. Some older commits have no
  scope. New commits must have one.
- The repository allows only merge commits. It deletes the source branch after
  a merge. It allows auto-merge. It has no PR template.
- The `master protection` ruleset protects `master`. It requires the `build`
  check and allows only merge commits. It blocks deletion and force pushes.
- The ruleset requires one approving review. An open "changes requested"
  review also blocks the merge. GitHub does not let the PR author approve
  their own PR.
- CodeRabbit reviews every PR and can request changes. It approves only after
  all of its threads are resolved, and its approval counts as the required
  review. `.coderabbit.yaml` holds its configuration. Its title check fails a
  PR title that does not use the Conventional Commits format.
- If you fix a CodeRabbit finding, reply in its thread. Do not post
  `@coderabbitai approve` or `@coderabbitai resolve` to get an approval.
- A human approves and merges every PR into `master`. Do not approve, merge,
  or enable auto-merge for these PRs.
- In a PR into `master`, use `Closes #<id>` only for issues that the PR
  delivers.

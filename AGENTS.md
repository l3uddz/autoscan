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
| `autoscan.go` | Core types: `Scan`, `ProcessorFunc`, `Target`, `ErrTargetUnavailable`, rewriters, filters |
| `cmd/autoscan/` | CLI entry point, configuration, HTTP router, trigger and target wiring |
| `triggers/<name>/` | One package for each trigger (Sonarr, Radarr, Readarr, Lidarr, manual, inotify) |
| `targets/<name>/` | One package for each target (Plex, Emby, Jellyfin, autoscan) |
| `processor/` | Scan queue and SQLite datastore |
| `processor/migrations/` | Embedded SQL migrations for the datastore |
| `migrate/` | Migration runner |
| `docker/Dockerfile` | Container image, built from the goreleaser output in `dist/` |

## Code changes

### Code quality

Apply SOLID, KISS, DRY, and YAGNI to each code change.

- Give each package and function a clear responsibility. Keep interfaces small
  and define them where callers use them.
- Prefer direct control flow and names that explain intent.
- Reuse existing code when it implements the same rule. Keep separate rules
  independent even if their code looks similar.
- Add abstractions, configuration, and dependencies only for current needs.
- Before delivery, review the diff for duplicated rules, unnecessary
  abstraction, and unused code. Fix the problems that the change introduces.
- Preserve behavior outside the requested scope. Keep input validation and
  error handling that prevents lost work.

### Repository conventions

- If you add a trigger, add a package under `triggers/` and add its
  configuration to `cmd/autoscan/main.go`. Its `New` function takes the
  configuration and an `autoscan.ProcessorFunc`. A webhook trigger returns an
  `http.Handler`. Use `triggers/webhook` for it, and register it in
  `cmd/autoscan/router.go`. A background trigger, such as inotify, returns a
  value with a `Run` method that runs in the background. Start it in
  `cmd/autoscan/main.go`.
- If you add a target, add a package under `targets/`. Register it in
  `cmd/autoscan/main.go`. Send its HTTP requests with the client in
  `targets/internal/httpapi`.
- Put webhook test payloads in the `testdata/` directory of the trigger package.
- If you change the datastore schema, add a new numbered file such as
  `2_<name>.sql` to `processor/migrations/`. Do not edit a migration that a
  release already contains.
- If you change user-facing behavior or configuration, update `README.md`.
- The Go version comes from `go.mod`. Do not lower it. `mise.toml` pins the
  same minor release for local work and CI. If you change one, change both.

## Checks

You need [mise](https://mise.jdx.dev). Run `mise install` once to install
the Go and goreleaser versions from `mise.toml`. CI installs the same
versions. Run `mise tasks` to list the tasks.

The required `build` job runs the following commands in order before
packaging. Run the same commands before you push.

```sh
mise run vendor
mise run test
mise run lint
```

`mise run vendor` runs `go mod tidy` and then creates `vendor/`. Git ignores
`vendor/`. Do not commit it. If `go mod tidy` changes `go.mod` or `go.sum`,
commit those changes with the change that caused them. If `go mod tidy`
changes the committed files, CI fails.

`mise run lint` runs `gofmt` on tracked Go files, then `go vet ./...`, and then a
pinned `staticcheck`. If `gofmt` lists a file, the task fails. A lint failure
fails the `build` job.

A separate `govulncheck` job in the `ci` workflow checks for known
vulnerabilities. It is not a required check, so a new advisory does not block
unrelated PRs. Look at its result in each PR.

The workflows pin each action to a commit SHA, with the version in a comment.
No bot updates them. To update an action, take the SHA of the new tag from
`gh api repos/<owner>/<repo>/commits/<tag>`. Change the SHA and the comment
together.

`mise run build` makes a local binary in `dist/`.
`mise run snapshot` makes the release binaries in `dist/` with goreleaser.

## CI side effects

The `ci` workflow in `.github/workflows/ci.yml` runs on pushes to
`master`, on `v*` tags, and on PRs into `master`. A push to another branch,
such as `fix/<issue>-<slug>`, runs CI only after you open a PR into `master`.

- If a run is not from a fork, CI publishes a public Docker image tag,
  `l3uddz/autoscan:<branch>`. For a PR, the image contains the PR merge
  commit. `.github/scripts/docker-branch-tag.sh` makes the tag from the
  branch name. Each character that a Docker tag does not allow, such as `/`,
  becomes a hyphen. A tag longer than 128 characters is cut and gets a hash
  suffix.
- Releases own the tag `latest` and the tags that start with a digit. If a
  branch name maps to one of these tags, the build fails. Do not give a
  branch such a name.
- If two branches on origin map to the same tag, such as `fix/a` and
  `fix-a`, the build and the cleanup both fail.
- A new run cancels an older run for the same ref. Only one ref publishes
  each branch tag: `master` for pushes, or the PR for another branch. An
  older commit then cannot replace the image of a newer commit.
- If you delete a branch, the `docker-cleanup` workflow removes its Docker
  tag. It uses the same script as the build.
- If someone pushes a `v*` tag, the `release` job runs instead of `build`.
  It publishes a GitHub release and the Docker tags `<version>` and
  `latest`. Do not create or push tags unless the user asks for a release.
- The `build` and `release` jobs share one list of steps through a YAML
  anchor. Only the `release` job can write repository contents. Every other
  job only reads.

## Delivery

- The default branch is `master`. It is the only integration branch.
- Start each change on a short-lived branch from `master`. Name it
  `<type>/<issue>-<slug>`, for example `fix/99-sonarr-windows-paths`. Use the
  commit type that fits the change, such as `feat`, `fix`, `docs`, `build`,
  or `ci`.
- Track work in GitHub issues on this repository. If no issue exists, create
  one before you start, or ask the user. Do not invent an issue number.
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
- Contributors do not approve, merge, or enable auto-merge for a PR into
  `master`. The maintainer, `l3uddz`, merges it after CodeRabbit approves and
  the `build` check passes.
- One exception applies only to the maintainer's own PRs. If `l3uddz` opened
  the PR, an agent that works for the maintainer enables auto-merge with a
  merge commit. GitHub then merges the PR after CodeRabbit approves and the
  `build` check passes. The agent still never approves a PR and never
  bypasses the ruleset.
- In a PR into `master`, use `Closes #<id>` only for issues that the PR
  delivers.

## Commits and pull requests

The repository merges with merge commits, so every commit on a branch stays
in the history of `master`. Each commit subject also becomes one line of the
release notes. The `changelog` section of `.goreleaser.yml` groups the
subjects by type: breaking changes, features, bug fixes, performance, and
other changes. It leaves out `docs` and `test` commits and merge commits. It
reads only the subject line. Write each subject for a user who reads the
release notes.

### Commit messages

Use the Conventional Commits format for every commit subject:

```text
<type>(<scope>)[!]: <summary>
```

- The type is one of `feat`, `fix`, `docs`, `refactor`, `perf`, `test`,
  `build`, `ci`, `chore`, `style`, or `revert`. Choose it from the change,
  not from the branch name. `feat` is new behavior, `fix` is a bug fix, and
  `docs` is documentation only.
- The scope is required and lowercase. It names the affected area with one
  word, for example `processor`, `migrate`, `cmd`, `config`, `targets`,
  `plex`, `emby`, `jellyfin`, `triggers`, `sonarr`, `radarr`, `lidarr`,
  `readarr`, `manual`, `inotify`, `webhook`, `docker`, `unraid`, `build`,
  `release`, `tooling`, `readme`, or `agents`. Use `targets` or `triggers`
  for a change to several packages in that directory. Some older commits
  have no scope or a scope such as `triggers/sonarr`. New commits use one
  of the forms above.
- The summary is a short imperative phrase with no trailing period, for
  example `handle missing library sections`. The whole subject has at most
  72 characters.
- If a commit makes a breaking change, put `!` before the colon, for
  example `feat(config)!: rename the plex section`. Also explain the impact
  and the migration in a `BREAKING CHANGE:` footer. The release notes read
  only the subject. Without `!`, the commit is not listed under breaking
  changes.
- Put the reason for the change in the body, after a blank line. Reference
  only real issues. Keep closing keywords such as `Closes #<id>` out of
  commit messages.
- Make one change in each commit. A follow-up fix for a review finding is
  its own `fix` or `docs` commit, and it appears in the release notes too.
- Check each new commit subject before you push. After you push a branch,
  do not rewrite its commits and do not force-push it.

### Merge commits

Keep the default merge commit title. On GitHub, it is `Merge pull request
#<id> from <branch>`. For a local merge, it is the `Merge branch '<branch>'`
subject that git writes. Do not write a custom merge commit title, also not
one in the Conventional Commits format. The release notes leave out only the
commits whose subject starts with `Merge`.

### Pull requests

- The PR title uses the same format as a commit subject. The CodeRabbit
  title check fails any other title.
- Write the description for a reader who did not see your work:
  - the problem
  - the change and the resulting behavior
  - the checks that you ran and their results
  - the source branch and the target branch
  - `Closes #<id>` for each issue that the PR delivers
- Keep local checks, live checks on a real media server, and acceptance that
  is still open apart. Do not claim a check that did not run.
- Do not put tokens, server addresses, or other private values in a commit,
  a PR description, or a comment.
- If the scope of a PR changes, rewrite its title and description.

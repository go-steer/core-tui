# core-tui project memory

When an AGENTS.md-aware agent runs inside this repo, this file is
loaded into the agent's system prompt as the project-level
instruction prefix. Keep it short and load-bearing.

## What this project is

`core-tui` is a standalone, reusable Bubble Tea TUI for agentic
assistants — extracted from the duplicated TUI code in
[`go-steer/cogo`](https://github.com/go-steer/cogo) and
[`go-steer/core-agent`](https://github.com/go-steer/core-agent) so
hosts can depend on a single library going forward.
[`go-steer/core-agent`](https://github.com/go-steer/core-agent) is the
reference host and the only one that gates a release; cogo is a
historical source, not a gating consumer (`docs/design.md` §8).

It is intentionally agent-framework agnostic: nothing under `tui/`
imports an LLM SDK, an MCP SDK, or an agent runtime. The integration
seam with a host is a small Go interface set documented in
[`docs/design.md`](./docs/design.md) §3.

**Source of truth.** Read these two docs before changing anything
that touches the public surface:

- [`docs/requirements.md`](./docs/requirements.md) — user-visible
  behavior the library must deliver.
- [`docs/design.md`](./docs/design.md) — module layout, the plug-in
  surface, lifecycle, and the test strategy.

When you need to know whether a given exported symbol is contract or
incidental, when it shipped, or what its doc comment actually says, use
[`docs/api-audit.md`](./docs/api-audit.md) — it inventories all 223
exported symbols and 438 members with provenance. It is generated, so
treat it as a snapshot (audit date is in its header), not a live index.

## Layout

```
tui/                  the library; public API per design.md §3.
                      flat package — Model + Update/View + slash
                      commands + palette + permissions modal +
                      elicit modal + markdown renderer + transcript.
  testagent/          scripted agent fixture — tests + examples/local.
examples/             runnable host examples.
  local/              scripted agent + every capability wired; the
                      visual harness (ctrl+y permission, ctrl+e elicit,
                      ctrl+x tool detail, /switch, -verbose-tools).
  notifier-smoke/     standalone Notifier-contract exerciser.
dev/                  build / test / lint tooling — see dev/README.md.
docs/                 source-of-truth design docs (requirements,
                      design, decisions).
docs/site/            published Hugo+Docsy site.
.github/workflows/    thin delegators to dev/ci/presubmits/.
CHANGELOG.md          every release back to v0.1.0 + the stability
                      promise. Promoted at tag time (see below).
```

Not here yet: `examples/core-agent/` (the reference-host adapter
sketch, issue #82) — with one gating host it would be the only
compile-time canary on the plug-in surface.

## Build & test

```bash
dev/tools/ci          # full local CI in fast-fail order
dev/tools/build       # go build ./...
dev/tools/test-unit   # go test -race -coverprofile, all packages
dev/tools/lint-go     # golangci-lint (auto-installs the pinned version)
dev/tools/fix-go-format  # auto-fix gofmt + goimports
```

The default test run needs no network and no API keys.

## Conventions

- **Plan before non-trivial work.** Anything that touches the §3
  plug-in surface in `docs/design.md` gets discussed in an issue (or a
  design doc under `docs/`) before code lands. Both named hosts depend
  on that surface — breaking it has downstream blast radius.
- **License headers everywhere.** The full Apache 2.0 boilerplate
  attributed to The go-steer team sits at the top of every Go / shell
  / YAML / Python source file. The `goheader` linter inside
  `dev/tools/lint-go` enforces this on `.go` files; for new shell /
  YAML / Python files, run `dev/tools/add-license-headers` (idempotent).
- **Small, self-contained commits with informative bodies.** Subject
  lines follow Conventional Commits (`feat:`, `fix:`, `docs:`,
  `chore:`, `refactor:`, `test:`, `ci:`, `build:`). Bodies explain
  *why* and call out the verification done.
- **No Co-Authored-By trailer.** Maintainer preference — author the
  work under your own name. DCO sign-off (`git commit -s`) is the
  expected practice; see [`CONTRIBUTING.md`](./CONTRIBUTING.md). This
  applies to commits, PR titles, and PR bodies — no Claude / Claude
  Code / "Generated with" / Co-Authored-By attribution anywhere.
- **Tests before merging.** Every new file ships with unit tests
  driven by direct `Update(msg)` calls plus `History.Snapshot()` /
  modal-state assertions. A new feature without a test is not done.
  Target ≥ 70% statement coverage in `package tui` per
  `docs/requirements.md` §N-TEST.
- **Errors flow to the user.** Render-time failures surface as a
  system or error message in the chat; never panic and never silently
  drop. The TUI must remain interactive after any single-turn error.
- **Capabilities are opt-in.** Every advanced feature beyond
  `Agent.Run` is a capability the TUI feature-detects via type
  assertion. A missing capability degrades to a "not available in
  this host" message — never a hard error.
- **Charm v2 only.** Imports are `charm.land/bubbletea/v2`,
  `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`,
  `charm.land/glamour/v2`, `charm.land/huh/v2`. No `github.com/charmbracelet/...`
  imports — those are the v1 line. Background-color handling uses
  `tea.BackgroundColorMsg`; do not call into the v1 termenv-based
  query path. See [`docs/decisions.md` D2](./docs/decisions.md#d2-bubble-tea-major-version)
  for the rationale.

## How we develop

Single long-lived branch: `main`. Work happens on short-lived feature
branches (`feat/...`, `fix/...`, `chore/...`, `docs/...`) → PR
against `main` → merge once CI's required status checks are green.
Branch protection on `main` requires `test`, `lint`,
`go mod tidy is clean` and `govulncheck`, requires a linear history,
and requires the branch to be up to date with `main` before merging.
Those four checks always report: `ci.yml` runs on every PR, and its
`changes` job short-circuits the expensive steps when the diff is
docs-only — every changed file Markdown or under `docs/` — so a docs
PR goes green in seconds without the full Go pipeline. Commits are
DCO-signed off (`git commit -s`) and
follow Conventional Commits — see [`CONTRIBUTING.md`](./CONTRIBUTING.md)
for the full contributor flow + DCO walkthrough.

Conventions worth knowing at agent prompt time:

- **Run presubmits before every push.** `dev/ci/presubmits/*` are the
  same scripts CI runs. Full sweep:
  `dev/ci/presubmits/{build,lint-go,test-unit,verify-go-format,verify-mod-tidy,vet,verify-vuln}`,
  plus `verify-coverage` (needs the `coverage.out` that `test-unit`
  writes) and `verify-apidiff`. Or just `dev/tools/ci`, which runs all of
  them in that order.
- **Don't break the exported API by accident.** `verify-apidiff` diffs
  `package tui` against the last release tag. A deliberate break is
  acknowledged in `dev/api-breaks.txt` by the PR that makes it — see
  "Changing the exported API" in CONTRIBUTING.md.
- **Rebase, don't merge.** Feature branches stay rebased on `main`.
  `git push --force-with-lease` on your own branches is normal; never
  force-push `main`.
- **Stacked PRs.** When `feat/B` depends on `feat/A`, base PR B on
  branch A. Retarget downstream PRs to `main` BEFORE merging the
  parent (`gh pr edit B --base main`), then merge A. Rebase the
  downstream onto new main after each parent lands
  (`git rebase --onto origin/main <old-parent-sha>`) to skip the
  squashed commit from history.
- **Design docs before non-trivial work.** Anything bigger than a
  small fix gets a `docs/<feature>-design.md` with a "Settled
  decisions (do not relitigate)" section + explicit "Out of scope"
  list. Settled-decisions framing keeps follow-up reviews from
  re-relitigating the same trade-offs.
- **Hugo site walks alongside the design docs.** User-visible changes
  update the published site at `docs/site/content/docs/` in the same
  PR as the code, not as a follow-up.

## How we release

SemVer: minor bump (`v0.X.0`) pre-1.0 for any release; patch
(`v0.X.Y`) for fix-only releases. Breaking changes land on minor
bumps with a one-version deprecation period when feasible. v1.0 is
declared once the reference host (core-agent) has been migrated and
stayed green for one minor release AND the pre-freeze work in the
[v1.0 milestone](https://github.com/go-steer/core-tui/milestone/1) has
landed (per `docs/design.md` §8). cogo is not a gating consumer.

Recipe:

1. **Branch** `release/vX.Y.Z` off main once the feature PRs are
   merged.
2. **Promote** `[Unreleased]` → `[X.Y.Z] — YYYY-MM-DD` in
   `CHANGELOG.md` with a one-paragraph summary, the `### Added` /
   `### Fixed` / `### Security` entries you've been accumulating
   under it, then a fresh empty `[Unreleased]` above.
3. **Bump the README pin** to `go get github.com/go-steer/core-tui@vX.Y.Z`.
4. **PR** the release commit (`docs: promote [Unreleased] to [X.Y.Z]
   + bump release pin`). Docs-only CI satisfies branch protection
   without the full Go pipeline.
5. **Admin-squash-merge** once docs-only CI is green.
6. **Tag** on the merge SHA:
   `git tag -a vX.Y.Z <sha> -m "vX.Y.Z — <theme>"` then
   `git push origin vX.Y.Z`. `pkg.go.dev` picks up the tag
   automatically — no goreleaser, no binary.
7. **Create the GH release** with
   `gh release create vX.Y.Z --title --notes-file <file>` where
   `<file>` is the `[X.Y.Z]` CHANGELOG section.

Accumulate the `### Added` / `### Fixed` / `### Security` entries under
`[Unreleased]` **as each PR lands**, not at tag time — then step 2 is
mechanical and nothing gets reconstructed from `git log` afterwards.

## Status

Shipping. Latest release is tracked in [`CHANGELOG.md`](./CHANGELOG.md);
`package tui` is complete enough that core-agent deleted its
`internal/tui/` and depends on this library (as of v0.18.0).

Pre-1.0. What's left before the API freezes is the
[v1.0 milestone](https://github.com/go-steer/core-tui/milestone/1):
capability-surface consolidation, the exported-surface audit, removing
the vestigial render paths, an `apidiff` gate in CI, headless smoke
tests, the `examples/core-agent` adapter, coverage to the ≥70% floor,
and macOS in the CI matrix. Read the milestone before proposing
anything that widens the exported surface — it is being narrowed, not
grown.

# Contributing to core-tui

Thanks for your interest in contributing! This file is the table of contents — most of the detail lives in [`dev/README.md`](./dev/README.md) and the [docs site](https://go-steer.github.io/core-tui/).

By participating in this project you agree to abide by the [Code of Conduct](./CODE_OF_CONDUCT.md).

## Reporting bugs and requesting features

- **Bugs:** [open an issue](https://github.com/go-steer/core-tui/issues/new) and include your OS, terminal (iTerm, Terminal.app, kitty, alacritty, GNOME terminal, tmux, screen, …), Go version, and the smallest set of steps that reproduces the problem. A short asciinema or a copy of the rendered viewport helps a lot.
- **Feature requests:** check [`docs/requirements.md`](./docs/requirements.md) and the [open issues](https://github.com/go-steer/core-tui/issues) first — your idea may already be planned. If not, file an issue with the use case (what you're trying to do with which host agent) before the proposed solution.
- **Questions / discussion:** [GitHub Discussions](https://github.com/go-steer/core-tui/discussions).

## Pull requests

### Before you start

For anything beyond a typo fix or one-line bug, open an issue first so we can agree on the approach. PRs that are aligned upfront merge faster than ones that surface a design disagreement at review time.

The library's stable surface is documented in [`docs/design.md`](./docs/design.md) §3 (the `Agent` interface + capability interfaces + `Options` + the `Prompter`/`Elicitor` pair). Changes that touch this surface need to be discussed before implementation — both named hosts (`cogo`, `core-agent`) depend on it.

### Workflow

1. Fork and create a short-lived feature branch off `main` (e.g. `feat/slash-provider`, `fix/palette-scroll`, `docs/install-snippet`).
2. Make your change. Keep the diff focused; unrelated cleanup belongs in a separate PR.
3. Run the full local CI before pushing:
   ```bash
   dev/tools/ci
   ```
   This is the same script that runs in GitHub Actions — green locally means green remotely. See [`dev/README.md`](./dev/README.md) for the full layout and how to add new checks.
4. Open the PR against `main`. CI runs on the PR; the four required status checks (`test`, `lint`, `go mod tidy is clean`, `govulncheck`) gate the merge. Docs-only PRs satisfy these checks via a companion no-op workflow without running the full Go pipeline.

### Commit messages — Conventional Commits

Subject lines follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — user-visible new functionality
- `fix:` — user-visible bug fix
- `docs:` — documentation only
- `test:` — tests only
- `refactor:` — code change that's neither a feature nor a fix
- `chore:` / `build:` / `ci:` — repo plumbing

Optional scope in parens: `feat(palette): …`, `fix(elicit): …`. Keep the subject under ~70 chars; put detail in the body explaining *why* and what verification you did.

### Developer Certificate of Origin (DCO)

All commits must be **signed off** under the [Developer Certificate of Origin](https://developercertificate.org/). The DCO is a lightweight assertion that you wrote the patch (or have the right to submit it under the project's Apache-2.0 license) — it's a `Signed-off-by:` trailer in the commit message, not a cryptographic signature.

Sign off by passing `-s` to `git commit`:

```bash
git commit -s -m "feat(palette): add fuzzy matching to /-palette"
```

…which appends:

```
Signed-off-by: Your Name <you@example.com>
```

The name and email must match your `git config user.name` / `user.email`. If you forget, amend with `git commit --amend -s` (single commit) or rebase with `-x 'git commit --amend -s --no-edit'` (multiple).

**No `Co-Authored-By` trailers.** Author the work under your own name; DCO sign-off is the expected practice. This applies to commits, PR titles, and PR bodies.

### License headers

Every source file carries the full Apache 2.0 header attributed to The go-steer team:

```
// Copyright 2026 The go-steer team.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
```

`golangci-lint` enforces this on `.go` files automatically via the `goheader` linter. For new shell, YAML, or Python files, run `dev/tools/add-license-headers` once — it's idempotent and normalizes any existing header to the canonical form. See [`dev/README.md`](./dev/README.md#license-headers) for the full rules.

### Tests

- Unit tests live next to the code (`*_test.go`) and drive the `bubbletea.Model` via direct `Update(msg)` calls plus assertions on history / palette / modal state.
- Smoke tests run a headless `tea.Program` against the bundled `examples/local/` and `examples/permissions/` fixtures.
- A new feature without a test is not done. A new bug fix without a regression test makes it easy for the bug to come back.
- Target ≥ 70% statement coverage in `package tui` (per [`docs/requirements.md`](./docs/requirements.md) §N-TEST).

## Project layout

- `tui/` — the library; public API per [`docs/design.md`](./docs/design.md) §3.
- `examples/` — minimal host adapter examples (`local`, `permissions`, plus host-specific sketches).
- `dev/` — local + CI tooling (run from here, don't reinvent).
- `docs/` — source-of-truth design docs (`requirements.md`, `design.md`, `decisions.md`).
- `docs/site/` — Hugo source for the published documentation site.
- `.github/workflows/` — thin delegators to `dev/ci/presubmits/`.

For deeper context on conventions and gotchas, read [`AGENTS.md`](./AGENTS.md).

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](./LICENSE).

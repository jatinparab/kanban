# kanban

[![CI](https://github.com/jatinparab/kanban/actions/workflows/ci.yml/badge.svg)](https://github.com/jatinparab/kanban/actions/workflows/ci.yml)
[![skills.sh](https://skills.sh/b/jatinparab/kanban)](https://skills.sh/jatinparab/kanban)

**Durable plans for coding agents.** A tiny, local-first CLI that turns long-running work into independently verifiable Markdown tickets—without putting board state in your repository.

- Canonical boards live outside the repo; `.kanban` is only a symlink.
- Boards are isolated by Git worktree.
- Tickets are readable Markdown with YAML frontmatter.
- Every command supports structured `--json` output.
- No server, account, database, or network connection required.

## Install

Install the latest verified binary **and** teach supported coding agents how to use it:

```sh
curl -fsSL https://raw.githubusercontent.com/jatinparab/kanban/main/install.sh | sh
```

The installer supports Linux and macOS on x86-64 and ARM64. It installs the CLI to `~/.local/bin` and installs the `kanban` agent skill through [skills.sh](https://skills.sh/).

```sh
# Options
./install.sh --version v1.2.3
./install.sh --bin-dir /usr/local/bin
./install.sh --no-skill
./install.sh --skill-only
```

Install only the skill directly:

```sh
npx skills add jatinparab/kanban --skill kanban -g
```

Or build from source with Go 1.22+:

```sh
go install github.com/jatinparab/kanban@latest
```

## Quick start

```sh
kanban init "Release work"
kanban task create "Investigate failure" --body "Capture logs and identify the cause."
kanban task --id 1 status IN_PROGRESS
kanban status
kanban task --id 1 show
```

Run `kanban help` for the complete command reference. Set `KANBAN_HOME` to override canonical storage. See [SPEC.md](SPEC.md) for the v1 behavior and data format.

## Releasing

Push a semantic version tag. GitHub Actions runs GoReleaser and publishes release notes, Linux/macOS archives, and SHA-256 checksums.

```sh
git tag v1.0.0
git push origin v1.0.0
```

# agent-kanban specification (v1)

## Purpose

`kanban` is a lightweight, local-first CLI for agents to manage Markdown tickets in a per-project board. The canonical board data is outside the repository; a worktree only receives an optional `.kanban` symlink.

The five valid ticket statuses are exactly:

- `TODO`
- `BLOCKED`
- `IN_PROGRESS`
- `IN_REVIEW`
- `DONE`

Status input is case-insensitive and is stored and displayed in uppercase. All transitions between valid statuses are allowed.

## Platform and installation

- Implement in Go and distribute as a single `kanban` executable.
- v1 supports Linux and macOS.
- It must operate without a network connection, daemon, database, or Git write operations.

## Canonical storage

The board home is selected in this order:

1. `KANBAN_HOME`, if nonempty. Its value is the complete board-home path.
2. macOS: `~/Library/Application Support/kanban`
3. Other supported systems: `${XDG_DATA_HOME}/kanban`, or `~/.local/share/kanban` when `XDG_DATA_HOME` is unset.

Layout:

```text
<kanban-home>/
  boards/
    <board-id>/                 # one active board
      board.json
      tasks/
        0001.md
        0002.md
  archives/
    <board-id>/
      <UTC timestamp>/          # an archived former active board
        board.json
        tasks/...
```

Canonical data in `<kanban-home>` is the sole source of truth. `.kanban` is only a convenience symlink and contains no unique data.

## Board identity

The CLI resolves the current location before every board-aware command:

1. If the current directory is inside a Git worktree, obtain its Git common directory, resolve it to a physical, absolute path, and use its parent project directory. The current worktree root is retained only as the location for that worktree's optional attachment.
2. Otherwise, resolve the current directory to a physical, absolute path.

The board ID is the lowercase hexadecimal SHA-256 of the UTF-8 string:

```text
kanban/v1:<kind>:<resolved-path>
```

`kind` is `git-project` in case 1 and `directory` in case 2. The full 64-character hash is the board directory name.

Consequences:

- All worktrees for a Git project share one board, including worktrees created for individual ticket branches.
- Commands from any subdirectory of a Git worktree locate that project's board.
- A non-Git board is identified by the exact directory from which it was initialized; its child directories are separate identities.

## Board metadata

`board.json` is UTF-8 JSON and contains at least:

```json
{
  "format_version": 1,
  "id": "<board-id>",
  "name": "Descriptive board name",
  "identity_kind": "git-project",
  "identity_path": "/physical/absolute/path",
  "created_at": "2026-07-12T12:34:56Z",
  "next_task_id": 1,
  "attachment_path": "/physical/absolute/path/.kanban"
}
```

`name` is required at initialization, must be nonempty after trimming, and is immutable in v1. `attachment_path` is omitted until attached. Timestamps are UTC RFC 3339 timestamps.

## Ticket files

Each ticket is one Markdown file at `tasks/%04d.md`, with YAML frontmatter followed by an unrestricted Markdown body:

```markdown
---
id: 1
title: Investigate lock handling
status: TODO
created_at: 2026-07-12T12:34:56Z
updated_at: 2026-07-12T12:34:56Z
---

Optional descriptive Markdown body.
```

Rules:

- IDs are positive, sequential decimal integers allocated from `next_task_id` and are never reused by the CLI.
- Titles are required, trimmed, and single-line.
- The body is optional and may be empty. The CLI never accepts a body value; use a file-editing tool to add or change it directly in the canonical Markdown file.
- The CLI owns the frontmatter fields. Users may edit the Markdown body directly, but malformed frontmatter, duplicate IDs, filename/ID disagreement, or unknown statuses are data errors.
- Ticket lists are ordered by numeric ID ascending.

## Worktree attachment

For a Git project, the attachment path is `<current-worktree-root>/.kanban`; each worktree may attach the shared board independently. For a non-Git board, it is `<identity-path>/.kanban`.

### `attach`

`kanban attach` requires an active board for the current identity. It creates `.kanban` as a symlink to the active canonical board directory and records its physical path in `board.json`.

When in a Git worktree, it also adds `.kanban` to that worktree's Git exclude configuration if it is not already present. It never edits a tracked `.gitignore`.

It fails safely if `.kanban` exists as a real file/directory or as a symlink to another destination. Repeating attach for the same correct symlink succeeds without changing data.

### `detach`

`kanban detach` removes only a `.kanban` symlink whose resolved target is the current identity's canonical board. It clears a matching recorded attachment path. If the path is absent or does not point to this board, it succeeds as a no-op. It never removes a regular file, directory, or unrelated symlink.

## Commands

`--json` is accepted by every command, including `help`, before or after command arguments.

### `kanban init "BOARD NAME"`

Creates a board using the resolved current identity and required descriptive name.

- Fails with `BOARD_ALREADY_EXISTS` if an active board directory already exists for the identity. The message tells the user to use `kanban archive` before starting a fresh board.
- Validates that the attachment path can be safely created, then creates metadata and `tasks/` and automatically performs the same attachment behavior as `kanban attach`. An attachment conflict leaves no newly initialized board behind.
- An archived board does not block initialization; a new active board with the same identity gets a new name and starts task IDs at 1.

### `kanban status`

For an active board, displays:

- board name, ID, identity path, and attachment state;
- counts for each of the five statuses; and
- every ticket's ID, status, and title.

If no active board exists for the current identity, it prints a friendly message equivalent to:

```text
No board here. Run `kanban init "BOARD NAME"` to start a board.
```

This is a successful result (exit code 0), not an error.

### `kanban task create "TITLE"`

Creates a new ticket in `TODO` with an empty Markdown body. It prints the created ticket ID and title. Add scope, verification criteria, and other body content directly to the canonical ticket file with a file-editing tool.

### `kanban task --id ID edit --title "TITLE"`

Edits one existing ticket title. Exactly one `--id` and one nonempty, single-line `--title` are required. The body is preserved and must be changed directly in the canonical Markdown file with a file-editing tool. A successful edit updates `updated_at`, writes the ticket atomically, and returns the updated ticket.

### `kanban task --id ID [--id ID ...] status STATUS`

Changes the status of one or more existing tickets. At least one `--id` is required. Duplicate supplied IDs are processed once. The command validates the status and **all** requested IDs before writing; if any requested ticket is missing or invalid, it changes none.

Example:

```sh
kanban task --id 1 --id 2 status done
```

### `kanban task --id ID [--id ID ...] show`

Shows complete details—including metadata and Markdown body—of each requested ticket, in ascending ID order. At least one `--id` is required.

### `kanban archive [--yes]`

Archives the active board for the current identity.

- Without `--yes`, it requires an interactive confirmation. It fails with `CONFIRMATION_REQUIRED` when stdin is not interactive.
- With confirmation, it safely removes the recorded matching `.kanban` symlink, then moves the entire active board directory to:
  `<kanban-home>/archives/<board-id>/<UTC timestamp>/`.
- Archived boards are never returned by `status`, task commands, or `attach` and cannot be attached through the CLI.
- Existing ticket files remain intact in the archive.

### `kanban help [COMMAND]`

Prints agent-oriented command documentation: purpose, syntax, arguments, statuses, storage model, JSON behavior, exit behavior, and examples. Without a topic it lists all commands; an optional topic provides focused help. `kanban --help` is equivalent to `kanban help`.

## Output and errors

Human output is concise, readable terminal text. JSON output always uses one stable top-level envelope:

```json
{"ok":true,"data":{}}
```

or:

```json
{"ok":false,"error":{"code":"MACHINE_READABLE_CODE","message":"Human-readable explanation"}}
```

Successful `status` without a board returns exit code 0 with data such as:

```json
{
  "ok": true,
  "data": {
    "board": null,
    "message": "No board here. Run `kanban init \"BOARD NAME\"` to start a board."
  }
}
```

Normal success returns 0. Invalid usage returns 2. Operational, validation, and data-integrity errors return nonzero (normally 1), including in JSON mode. Expected error codes include `BOARD_ALREADY_EXISTS`, `BOARD_NOT_FOUND`, `TASK_NOT_FOUND`, `INVALID_STATUS`, `INVALID_TICKET`, `ATTACHMENT_CONFLICT`, `CONFIRMATION_REQUIRED`, and `DATA_CORRUPT`.

## Safety and multi-agent behavior

All mutations (`init`, `attach`, `detach`, task create/edit/status, and archive) use a board-home local advisory write lock. They re-read relevant state while holding the lock.

Files are written using a temporary file in the destination directory, flushed, then atomically renamed. Multi-ticket status updates are validated before any ticket replacement; a failure leaves all ticket contents unchanged. Archive uses an atomic rename when source and archive are on the same filesystem (as this layout ensures).

Read commands do not mutate data. Symlink operations must resolve and validate destinations before removal, so the CLI never deletes paths outside its own matching attachment.

## Explicit v1 exclusions

- No remote synchronization, collaboration server, database, or background process.
- No board rename.
- No editor-launching interface or CLI body editing; use a file-editing tool on the canonical Markdown ticket file.
- No labels, assignees, priorities, dependencies, search, filtering, or status-transition restrictions.
- No automatic migration or recovery of manually corrupted board data.

---
name: kanban
description: Manage longer-horizon, multi-step work as locally persisted Markdown tickets with independently verifiable outcomes. Use when work must survive context switches or coordinate multiple agents. Do not use for a lightweight todo list or checklist within a single step.
compatibility: Requires the kanban CLI on PATH.
---

# Kanban

Use this only for longer-horizon work whose tasks are individually verifiable. Do **not** turn intermediate actions within one task into tickets; use normal notes or an in-context checklist instead.

1. Run `kanban status`. If no board exists, run `kanban init "DESCRIPTIVE BOARD NAME"`.
2. Create outcome-oriented tickets with verification criteria in the body:
   ```sh
   kanban task create "OUTCOME" --body "Scope and how to verify completion."
   ```
3. Keep state current:
   ```sh
   kanban task --id ID status IN_PROGRESS
   kanban task --id ID status IN_REVIEW
   kanban task --id ID status DONE
   ```
   Use `BLOCKED` when progress depends on something unresolved.
4. Before resuming or reporting, run `kanban status`; inspect details with `kanban task --id ID show`.

Use `--json` for structured automation. For attachment, archive, or command details, run `kanban help` or `kanban help COMMAND` rather than guessing.

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func inTempWorkDir(t *testing.T) (string, *application) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return root, &application{home: filepath.Join(root, "store"), out: io.Discard, in: strings.NewReader("")}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestGitWorktreesShareProjectBoard(t *testing.T) {
	sandbox := t.TempDir()
	project := filepath.Join(sandbox, "project")
	worktree := filepath.Join(sandbox, "ticket-worktree")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, project, "init")
	runGit(t, project, "config", "user.email", "test@example.com")
	runGit(t, project, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(project, "README"), []byte("test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, project, "add", "README")
	runGit(t, project, "commit", "-m", "initial")
	runGit(t, project, "worktree", "add", "-b", "ticket", worktree)

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	app := &application{home: filepath.Join(sandbox, "store"), out: io.Discard, in: strings.NewReader("")}

	if err = os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	projectID, err := resolveIdentity()
	if err != nil {
		t.Fatal(err)
	}
	physicalProject, err := physicalPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if projectID.Kind != "git-project" || projectID.Path != physicalProject {
		t.Fatalf("unexpected project identity: %#v", projectID)
	}
	if _, _, err = app.run([]string{"init", "Shared project board"}); err != nil {
		t.Fatal(err)
	}

	if err = os.Chdir(worktree); err != nil {
		t.Fatal(err)
	}
	worktreeID, err := resolveIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if worktreeID.ID != projectID.ID || worktreeID.Path != projectID.Path {
		t.Fatalf("worktree resolved a different board: project=%#v worktree=%#v", projectID, worktreeID)
	}
	if _, _, err = app.run([]string{"task", "create", "Available from any worktree"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.run([]string{"attach"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(worktree, ".kanban")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("worktree attachment missing: mode=%v err=%v", st, err)
	}
}

func TestBoardLifecycle(t *testing.T) {
	root, app := inTempWorkDir(t)

	data, human, err := app.run([]string{"status"})
	if err != nil || human != noBoardMessage || data.(map[string]any)["board"] != nil {
		t.Fatalf("unexpected empty status: data=%#v human=%q err=%v", data, human, err)
	}
	if _, _, err = app.run([]string{"init", "Release work"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(root, "work", ".kanban")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("init did not attach board: mode=%v err=%v", st, err)
	}
	if _, _, err = app.run([]string{"task", "create", "First"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.run([]string{"task", "create", "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.run([]string{"task", "--id", "2", "--id", "1", "status", "in-review"}); err != nil {
		t.Fatal(err)
	}
	id, _ := resolveIdentity()
	tasks, err := readAllTickets(app.boardDir(id.ID))
	if err != nil || len(tasks) != 2 || tasks[0].Status != "IN_REVIEW" || tasks[0].Body != "" {
		t.Fatalf("unexpected tasks: %#v, %v", tasks, err)
	}
	if _, _, err = app.run([]string{"detach"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(root, "work", ".kanban")); !os.IsNotExist(err) {
		t.Fatalf("detach left attachment: %v", err)
	}
	if _, _, err = app.run([]string{"attach"}); err != nil {
		t.Fatal(err)
	}
	archiveData, _, err := app.run([]string{"archive", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archiveData.(map[string]any)["path"].(string)
	b, err := readBoard(filepath.Join(archivePath, "board.json"))
	if err != nil || b.AttachmentPath != "" {
		t.Fatalf("bad archived metadata: %#v, %v", b, err)
	}
	if _, err = os.Lstat(filepath.Join(root, "work", ".kanban")); !os.IsNotExist(err) {
		t.Fatalf("archive left attachment: %v", err)
	}
}

func TestMultiTaskStatusValidatesBeforeWriting(t *testing.T) {
	_, app := inTempWorkDir(t)
	if _, _, err := app.run([]string{"init", "Atomic updates"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.run([]string{"task", "create", "First"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := app.run([]string{"task", "--id", "1", "--id", "999", "status", "DONE"})
	var ae *appError
	if !errors.As(err, &ae) || ae.Code != "TASK_NOT_FOUND" {
		t.Fatalf("wanted TASK_NOT_FOUND, got %v", err)
	}
	id, _ := resolveIdentity()
	task, err := readTicket(app.boardDir(id.ID), 1)
	if err != nil || task.Status != "TODO" {
		t.Fatalf("existing task changed: %#v, %v", task, err)
	}
}

func TestTaskBodyFlagsAreRejected(t *testing.T) {
	_, app := inTempWorkDir(t)
	if _, _, err := app.run([]string{"init", "No CLI bodies"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"task", "create", "Original", "--body", "Details"},
		{"task", "create", "--body", "Details", "Original"},
	} {
		_, _, err := app.run(args)
		var ae *appError
		if !errors.As(err, &ae) || !ae.Usage {
			t.Fatalf("body flag should be a usage error: %v", err)
		}
	}
	if _, _, err := app.run([]string{"task", "create", "Original"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := app.run([]string{"task", "--id", "1", "edit", "--body", "Details"})
	var ae *appError
	if !errors.As(err, &ae) || !ae.Usage {
		t.Fatalf("body flag should be a usage error: %v", err)
	}
	id, _ := resolveIdentity()
	task, err := readTicket(app.boardDir(id.ID), 1)
	if err != nil || task.Body != "" {
		t.Fatalf("CLI changed ticket body: %#v, %v", task, err)
	}
}

func TestTaskEditTitleOnly(t *testing.T) {
	_, app := inTempWorkDir(t)
	if _, _, err := app.run([]string{"init", "Editing"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.run([]string{"task", "create", "Original"}); err != nil {
		t.Fatal(err)
	}
	id, _ := resolveIdentity()
	before, err := readTicket(app.boardDir(id.ID), 1)
	if err != nil {
		t.Fatal(err)
	}
	data, human, err := app.run([]string{"task", "--id", "1", "edit", "--title", "Renamed"})
	if err != nil {
		t.Fatal(err)
	}
	updated := data.(map[string]any)["task"].(ticket)
	if human != "Updated task 1: Renamed" || updated.Title != "Renamed" {
		t.Fatalf("unexpected output: %#v, %q", data, human)
	}
	after, err := readTicket(app.boardDir(id.ID), 1)
	if err != nil || after.Title != "Renamed" || after.Body != before.Body {
		t.Fatalf("title edit changed unexpected fields: %#v, %v", after, err)
	}
}

func TestParsePlainYAMLTitle(t *testing.T) {
	input := []byte("---\nid: 1\ntitle: A plain title\nstatus: TODO\ncreated_at: 2026-07-12T12:00:00Z\nupdated_at: 2026-07-12T12:00:00Z\n---\n\nBody\n")
	task, err := parseTicket(input)
	if err != nil || task.Title != "A plain title" || task.Body != "Body\n" {
		t.Fatalf("unexpected parse: %#v, %v", task, err)
	}
}

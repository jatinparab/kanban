package main

import (
	"errors"
	"io"
	"os"
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
	if _, _, err = app.run([]string{"task", "create", "First", "--body", "Details"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.run([]string{"task", "create", "--body", "More", "Second"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.run([]string{"task", "--id", "2", "--id", "1", "status", "in-review"}); err != nil {
		t.Fatal(err)
	}
	id, _ := resolveIdentity()
	tasks, err := readAllTickets(app.boardDir(id.ID))
	if err != nil || len(tasks) != 2 || tasks[0].Status != "IN_REVIEW" || tasks[0].Body != "Details\n" {
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

func TestParsePlainYAMLTitle(t *testing.T) {
	input := []byte("---\nid: 1\ntitle: A plain title\nstatus: TODO\ncreated_at: 2026-07-12T12:00:00Z\nupdated_at: 2026-07-12T12:00:00Z\n---\n\nBody\n")
	task, err := parseTicket(input)
	if err != nil || task.Title != "A plain title" || task.Body != "Body\n" {
		t.Fatalf("unexpected parse: %#v, %v", task, err)
	}
}

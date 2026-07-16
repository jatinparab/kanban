package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var statuses = []string{"TODO", "BLOCKED", "IN_PROGRESS", "IN_REVIEW", "DONE"}

const noBoardMessage = "No board here. Run `kanban init \"BOARD NAME\"` to start a board."

type appError struct {
	Code    string
	Message string
	Usage   bool
}

func (e *appError) Error() string { return e.Message }
func fail(code, format string, args ...any) error {
	return &appError{Code: code, Message: fmt.Sprintf(format, args...)}
}
func usage(format string, args ...any) error {
	return &appError{Code: "INVALID_USAGE", Message: fmt.Sprintf(format, args...), Usage: true}
}

type identity struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	AttachmentRoot string `json:"-"`
}

type board struct {
	FormatVersion  int    `json:"format_version"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	IdentityKind   string `json:"identity_kind"`
	IdentityPath   string `json:"identity_path"`
	CreatedAt      string `json:"created_at"`
	NextTaskID     int    `json:"next_task_id"`
	AttachmentPath string `json:"attachment_path,omitempty"`
}

type ticket struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Body      string `json:"body"`
}

type application struct {
	home    string
	jsonOut bool
	out     io.Writer
	in      io.Reader
}

func main() {
	jsonOut, args := stripJSON(os.Args[1:])
	home, err := kanbanHome()
	if err != nil {
		emitError(os.Stdout, jsonOut, fail("CONFIG_ERROR", "%v", err))
		os.Exit(1)
	}
	a := &application{home: home, jsonOut: jsonOut, out: os.Stdout, in: os.Stdin}
	data, human, err := a.run(args)
	if err != nil {
		emitError(a.out, jsonOut, err)
		var ae *appError
		if errors.As(err, &ae) && ae.Usage {
			os.Exit(2)
		}
		os.Exit(1)
	}
	if jsonOut {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": true, "data": data})
	} else if human != "" {
		fmt.Fprint(a.out, human)
		if !strings.HasSuffix(human, "\n") {
			fmt.Fprintln(a.out)
		}
	}
}

func stripJSON(args []string) (bool, []string) {
	out := make([]string, 0, len(args))
	found := false
	for _, s := range args {
		if s == "--json" {
			found = true
		} else {
			out = append(out, s)
		}
	}
	return found, out
}

func emitError(w io.Writer, jsonOut bool, err error) {
	ae := &appError{Code: "OPERATION_FAILED", Message: err.Error()}
	var got *appError
	if errors.As(err, &got) {
		ae = got
	}
	if jsonOut {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": ae.Code, "message": ae.Message}})
	} else {
		fmt.Fprintf(w, "Error: %s\n", ae.Message)
	}
}

func kanbanHome() (string, error) {
	if h := os.Getenv("KANBAN_HOME"); h != "" {
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "kanban"), nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "kanban"), nil
	}
	return filepath.Join(home, ".local", "share", "kanban"), nil
}

func (a *application) run(args []string) (any, string, error) {
	if len(args) == 0 {
		return a.help(nil)
	}
	if args[0] == "--help" || args[0] == "-h" {
		return a.help(args[1:])
	}
	switch args[0] {
	case "help":
		return a.help(args[1:])
	case "init":
		return a.init(args[1:])
	case "status":
		if len(args) != 1 {
			return nil, "", usage("usage: kanban status [--json]")
		}
		return a.status()
	case "attach":
		if len(args) != 1 {
			return nil, "", usage("usage: kanban attach [--json]")
		}
		return a.attach()
	case "detach":
		if len(args) != 1 {
			return nil, "", usage("usage: kanban detach [--json]")
		}
		return a.detach()
	case "archive":
		return a.archive(args[1:])
	case "task":
		return a.task(args[1:])
	default:
		return nil, "", usage("unknown command %q; run `kanban help`", args[0])
	}
}

func resolveIdentity() (identity, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return identity{}, fail("IDENTITY_ERROR", "get current directory: %v", err)
	}
	path, err := physicalPath(cwd)
	if err != nil {
		return identity{}, err
	}
	id := identity{Kind: "directory", Path: path, AttachmentRoot: path}

	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = cwd
	if b, e := rootCmd.Output(); e == nil {
		worktreeRoot, e := physicalPath(strings.TrimSpace(string(b)))
		if e != nil {
			return identity{}, e
		}
		commonCmd := exec.Command("git", "rev-parse", "--git-common-dir")
		commonCmd.Dir = worktreeRoot
		common, e := commonCmd.Output()
		if e != nil {
			return identity{}, fail("IDENTITY_ERROR", "resolve Git project: %v", e)
		}
		commonPath := strings.TrimSpace(string(common))
		if !filepath.IsAbs(commonPath) {
			commonPath = filepath.Join(worktreeRoot, commonPath)
		}
		commonPath, e = physicalPath(commonPath)
		if e != nil {
			return identity{}, e
		}
		// Git worktrees share a common .git directory. Its parent is the project
		// directory, so it remains stable when a ticket is worked on in another
		// worktree while attachments stay local to each worktree.
		id = identity{Kind: "git-project", Path: filepath.Dir(commonPath), AttachmentRoot: worktreeRoot}
	}
	sum := sha256.Sum256([]byte("kanban/v1:" + id.Kind + ":" + id.Path))
	id.ID = hex.EncodeToString(sum[:])
	return id, nil
}

func physicalPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fail("IDENTITY_ERROR", "%v", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fail("IDENTITY_ERROR", "resolve path: %v", err)
	}
	return path, nil
}

func attachmentPath(id identity) string { return filepath.Join(id.AttachmentRoot, ".kanban") }

func (a *application) boardDir(id string) string  { return filepath.Join(a.home, "boards", id) }
func (a *application) boardFile(id string) string { return filepath.Join(a.boardDir(id), "board.json") }
func taskFile(dir string, id int) string {
	return filepath.Join(dir, "tasks", fmt.Sprintf("%04d.md", id))
}

func (a *application) withLock(fn func() error) error {
	if err := os.MkdirAll(a.home, 0700); err != nil {
		return fail("OPERATION_FAILED", "create kanban home: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(a.home, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail("OPERATION_FAILED", "open lock: %v", err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fail("OPERATION_FAILED", "lock kanban home: %v", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func readBoard(path string) (board, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return board{}, fail("BOARD_NOT_FOUND", "no active board for this location")
		}
		return board{}, fail("DATA_CORRUPT", "read board metadata: %v", err)
	}
	var out board
	if json.Unmarshal(b, &out) != nil || out.FormatVersion != 1 || out.ID == "" || strings.TrimSpace(out.Name) == "" || out.NextTaskID < 1 {
		return board{}, fail("DATA_CORRUPT", "invalid board metadata at %s", path)
	}
	return out, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".kanban-tmp-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(perm); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	return err
}

func writeBoard(path string, b board) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(path, data, 0600)
}

func attachmentState(id identity, dir string) (string, bool) {
	p := attachmentPath(id)
	target, err := os.Readlink(p)
	if err != nil {
		return p, false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	target, _ = filepath.Abs(target)
	dir, _ = filepath.Abs(dir)
	return p, filepath.Clean(target) == filepath.Clean(dir)
}

func validateAttachment(id identity, dir string) error {
	p, attached := attachmentState(id, dir)
	if attached {
		return nil
	}
	if _, err := os.Lstat(p); err == nil {
		return fail("ATTACHMENT_CONFLICT", "%s already exists and is not this board's symlink", p)
	} else if !os.IsNotExist(err) {
		return fail("ATTACHMENT_CONFLICT", "inspect %s: %v", p, err)
	}
	return nil
}

func createAttachment(id identity, dir string) error {
	if err := validateAttachment(id, dir); err != nil {
		return err
	}
	_, attached := attachmentState(id, dir)
	created := false
	if !attached {
		if err := os.Symlink(dir, attachmentPath(id)); err != nil {
			return fail("OPERATION_FAILED", "create attachment: %v", err)
		}
		created = true
	}
	if id.Kind == "git-project" {
		if err := addGitExclude(id.AttachmentRoot); err != nil {
			if created {
				_ = os.Remove(attachmentPath(id))
			}
			return fail("OPERATION_FAILED", "update Git exclude: %v", err)
		}
	}
	return nil
}

func addGitExclude(root string) error {
	cmd := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return err
	}
	p := strings.TrimSpace(string(b))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	data, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	perm := os.FileMode(0600)
	if st, statErr := os.Stat(p); statErr == nil {
		perm = st.Mode().Perm()
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ".kanban" {
			return nil
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte(".kanban\n")...)
	return writeAtomic(p, data, perm)
}

func (a *application) init(args []string) (any, string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return nil, "", usage("usage: kanban init \"BOARD NAME\" [--json]")
	}
	name := strings.TrimSpace(args[0])
	if strings.ContainsAny(name, "\r\n") {
		return nil, "", usage("board name must be one line")
	}
	id, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	var b board
	err = a.withLock(func() error {
		dir := a.boardDir(id.ID)
		if _, e := os.Stat(dir); e == nil {
			return fail("BOARD_ALREADY_EXISTS", "board already exists; use `kanban archive` to start fresh")
		} else if !os.IsNotExist(e) {
			return e
		}
		if e := validateAttachment(id, dir); e != nil {
			return e
		}
		if e := os.MkdirAll(filepath.Join(dir, "tasks"), 0700); e != nil {
			return fail("OPERATION_FAILED", "create board: %v", e)
		}
		ok := false
		defer func() {
			if !ok {
				_ = os.RemoveAll(dir)
			}
		}()
		now := time.Now().UTC().Format(time.RFC3339)
		b = board{FormatVersion: 1, ID: id.ID, Name: name, IdentityKind: id.Kind, IdentityPath: id.Path, CreatedAt: now, NextTaskID: 1, AttachmentPath: attachmentPath(id)}
		if e := writeBoard(a.boardFile(id.ID), b); e != nil {
			return fail("OPERATION_FAILED", "write board: %v", e)
		}
		if e := createAttachment(id, dir); e != nil {
			return e
		}
		ok = true
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"board": b, "attached": true}, fmt.Sprintf("Initialized board %q (%s)\nAttached at %s", b.Name, shortID(b.ID), b.AttachmentPath), nil
}

func (a *application) status() (any, string, error) {
	id, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	b, err := readBoard(a.boardFile(id.ID))
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) && ae.Code == "BOARD_NOT_FOUND" {
			return map[string]any{"board": nil, "message": noBoardMessage}, noBoardMessage, nil
		}
		return nil, "", err
	}
	tasks, err := readAllTickets(a.boardDir(id.ID))
	if err != nil {
		return nil, "", err
	}
	counts := map[string]int{}
	for _, s := range statuses {
		counts[s] = 0
	}
	for _, t := range tasks {
		counts[t.Status]++
	}
	_, attached := attachmentState(id, a.boardDir(id.ID))
	data := map[string]any{"board": b, "attached": attached, "counts": counts, "tasks": tasks}
	var h strings.Builder
	fmt.Fprintf(&h, "Board: %s\nID: %s\nPath: %s\nAttached: %t\n", b.Name, b.ID, b.IdentityPath, attached)
	fmt.Fprintf(&h, "Counts: TODO %d | BLOCKED %d | IN_PROGRESS %d | IN_REVIEW %d | DONE %d\n", counts["TODO"], counts["BLOCKED"], counts["IN_PROGRESS"], counts["IN_REVIEW"], counts["DONE"])
	if len(tasks) == 0 {
		h.WriteString("Tasks: none\n")
	} else {
		h.WriteString("Tasks:\n")
		for _, t := range tasks {
			fmt.Fprintf(&h, "  %d  %-11s  %s\n", t.ID, t.Status, t.Title)
		}
	}
	return data, h.String(), nil
}

func (a *application) attach() (any, string, error) {
	id, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	var b board
	err = a.withLock(func() error {
		var e error
		b, e = readBoard(a.boardFile(id.ID))
		if e != nil {
			return e
		}
		if e = createAttachment(id, a.boardDir(id.ID)); e != nil {
			return e
		}
		b.AttachmentPath = attachmentPath(id)
		if e = writeBoard(a.boardFile(id.ID), b); e != nil {
			return fail("OPERATION_FAILED", "write board: %v", e)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"board_id": b.ID, "path": b.AttachmentPath, "attached": true}, "Attached board at " + b.AttachmentPath, nil
}

func removeMatchingAttachment(id identity, dir string) (bool, error) {
	p, matching := attachmentState(id, dir)
	if !matching {
		return false, nil
	}
	if err := os.Remove(p); err != nil {
		return false, fail("OPERATION_FAILED", "remove attachment: %v", err)
	}
	return true, nil
}

func (a *application) detach() (any, string, error) {
	id, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	removed := false
	err = a.withLock(func() error {
		b, e := readBoard(a.boardFile(id.ID))
		if e != nil {
			return e
		}
		removed, e = removeMatchingAttachment(id, a.boardDir(id.ID))
		if e != nil {
			return e
		}
		if b.AttachmentPath == attachmentPath(id) {
			b.AttachmentPath = ""
			if e = writeBoard(a.boardFile(id.ID), b); e != nil {
				return fail("OPERATION_FAILED", "write board: %v", e)
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	msg := "Board was already detached"
	if removed {
		msg = "Detached board"
	}
	return map[string]any{"detached": true, "removed": removed}, msg, nil
}

func normalizeStatus(s string) (string, bool) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
	for _, valid := range statuses {
		if s == valid {
			return s, true
		}
	}
	return "", false
}

func encodeTicket(t ticket) ([]byte, error) {
	title, _ := json.Marshal(t.Title)
	var b bytes.Buffer
	fmt.Fprintf(&b, "---\nid: %d\ntitle: %s\nstatus: %s\ncreated_at: %s\nupdated_at: %s\n---\n", t.ID, title, t.Status, t.CreatedAt, t.UpdatedAt)
	if t.Body != "" {
		b.WriteByte('\n')
		b.WriteString(t.Body)
		if !strings.HasSuffix(t.Body, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.Bytes(), nil
}

func parseTicket(data []byte) (ticket, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return ticket{}, errors.New("missing YAML frontmatter")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return ticket{}, errors.New("unterminated YAML frontmatter")
	}
	front, body := rest[:end], rest[end+5:]
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}
	fields := map[string]string{}
	for _, line := range strings.Split(front, "\n") {
		p := strings.Index(line, ":")
		if p <= 0 {
			return ticket{}, errors.New("invalid frontmatter line")
		}
		key := strings.TrimSpace(line[:p])
		if _, ok := fields[key]; ok {
			return ticket{}, errors.New("duplicate frontmatter field")
		}
		fields[key] = strings.TrimSpace(line[p+1:])
	}
	if len(fields) != 5 {
		return ticket{}, errors.New("frontmatter must contain exactly id, title, status, created_at, updated_at")
	}
	id, err := strconv.Atoi(fields["id"])
	if err != nil || id < 1 {
		return ticket{}, errors.New("invalid id")
	}
	var title string
	rawTitle := fields["title"]
	if strings.HasPrefix(rawTitle, "\"") {
		if err = json.Unmarshal([]byte(rawTitle), &title); err != nil {
			return ticket{}, errors.New("invalid quoted title")
		}
	} else if len(rawTitle) >= 2 && rawTitle[0] == '\'' && rawTitle[len(rawTitle)-1] == '\'' {
		title = strings.ReplaceAll(rawTitle[1:len(rawTitle)-1], "''", "'")
	} else {
		title = rawTitle
	}
	if strings.TrimSpace(title) == "" || strings.ContainsAny(title, "\r\n") {
		return ticket{}, errors.New("invalid title")
	}
	status, ok := normalizeStatus(fields["status"])
	if !ok || status != fields["status"] {
		return ticket{}, errors.New("invalid status")
	}
	if _, err = time.Parse(time.RFC3339, fields["created_at"]); err != nil {
		return ticket{}, errors.New("invalid created_at")
	}
	if _, err = time.Parse(time.RFC3339, fields["updated_at"]); err != nil {
		return ticket{}, errors.New("invalid updated_at")
	}
	return ticket{ID: id, Title: title, Status: status, CreatedAt: fields["created_at"], UpdatedAt: fields["updated_at"], Body: body}, nil
}

func readTicket(dir string, id int) (ticket, error) {
	p := taskFile(dir, id)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return ticket{}, fail("TASK_NOT_FOUND", "task %d not found", id)
	}
	if err != nil {
		return ticket{}, fail("INVALID_TICKET", "read task %d: %v", id, err)
	}
	t, err := parseTicket(data)
	if err != nil || t.ID != id {
		if err == nil {
			err = errors.New("filename and id disagree")
		}
		return ticket{}, fail("INVALID_TICKET", "invalid task %d: %v", id, err)
	}
	return t, nil
}

func readAllTickets(dir string) ([]ticket, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil {
		return nil, fail("DATA_CORRUPT", "read tasks: %v", err)
	}
	out := []ticket{}
	seen := map[int]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".md")
		id, er := strconv.Atoi(base)
		if er != nil || id < 1 || e.Name() != fmt.Sprintf("%04d.md", id) {
			return nil, fail("INVALID_TICKET", "invalid ticket filename %s", e.Name())
		}
		t, er := readTicket(dir, id)
		if er != nil {
			return nil, er
		}
		if seen[t.ID] {
			return nil, fail("INVALID_TICKET", "duplicate task id %d", t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func parseTaskArgs(args []string) ([]int, []string, error) {
	ids := []int{}
	for i := 0; i < len(args); {
		if args[i] != "--id" {
			return ids, args[i:], nil
		}
		if i+1 >= len(args) {
			return nil, nil, usage("--id requires a positive integer")
		}
		n, e := strconv.Atoi(args[i+1])
		if e != nil || n < 1 {
			return nil, nil, usage("invalid task id %q", args[i+1])
		}
		ids = append(ids, n)
		i += 2
	}
	return ids, nil, nil
}

func uniqueSorted(ids []int) []int {
	m := map[int]bool{}
	for _, id := range ids {
		m[id] = true
	}
	out := make([]int, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func (a *application) task(args []string) (any, string, error) {
	if len(args) > 0 && args[0] == "create" {
		return a.taskCreate(args[1:])
	}
	ids, rest, err := parseTaskArgs(args)
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 || len(rest) == 0 {
		return nil, "", usage("usage: kanban task --id ID [--id ID ...] (show | status STATUS | edit --title TITLE)")
	}
	if rest[0] == "edit" {
		if len(ids) != 1 {
			return nil, "", usage("edit requires exactly one --id")
		}
		return a.taskEdit(ids[0], rest[1:])
	}
	ids = uniqueSorted(ids)
	switch rest[0] {
	case "show":
		if len(rest) != 1 {
			return nil, "", usage("show takes no arguments")
		}
		return a.taskShow(ids)
	case "status":
		if len(rest) != 2 {
			return nil, "", usage("status requires exactly one status")
		}
		return a.taskStatus(ids, rest[1])
	default:
		return nil, "", usage("unknown task operation %q", rest[0])
	}
}

func (a *application) taskCreate(args []string) (any, string, error) {
	if len(args) != 1 {
		return nil, "", usage("usage: kanban task create \"TITLE\"")
	}
	title := strings.TrimSpace(args[0])
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return nil, "", usage("task title must be a nonempty single line")
	}
	idn, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	var t ticket
	err = a.withLock(func() error {
		b, e := readBoard(a.boardFile(idn.ID))
		if e != nil {
			return e
		}
		dir := a.boardDir(idn.ID)
		if _, e = os.Stat(taskFile(dir, b.NextTaskID)); e == nil {
			return fail("DATA_CORRUPT", "task file for next id %d already exists", b.NextTaskID)
		} else if !os.IsNotExist(e) {
			return e
		}
		now := time.Now().UTC().Format(time.RFC3339)
		t = ticket{ID: b.NextTaskID, Title: title, Status: "TODO", CreatedAt: now, UpdatedAt: now}
		data, _ := encodeTicket(t)
		if e = writeAtomic(taskFile(dir, t.ID), data, 0600); e != nil {
			return fail("OPERATION_FAILED", "write task: %v", e)
		}
		b.NextTaskID++
		if e = writeBoard(a.boardFile(idn.ID), b); e != nil {
			_ = os.Remove(taskFile(dir, t.ID))
			return fail("OPERATION_FAILED", "update board: %v", e)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"task": t}, fmt.Sprintf("Created task %d: %s", t.ID, t.Title), nil
}

func (a *application) taskEdit(id int, args []string) (any, string, error) {
	if len(args) != 2 || args[0] != "--title" {
		return nil, "", usage("usage: kanban task --id ID edit --title \"TITLE\"")
	}
	title := strings.TrimSpace(args[1])
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return nil, "", usage("task title must be a nonempty single line")
	}

	idn, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	var updated ticket
	err = a.withLock(func() error {
		if _, e := readBoard(a.boardFile(idn.ID)); e != nil {
			return e
		}
		var e error
		updated, e = readTicket(a.boardDir(idn.ID), id)
		if e != nil {
			return e
		}
		updated.Title = title
		updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		data, e := encodeTicket(updated)
		if e != nil {
			return fail("OPERATION_FAILED", "encode task %d: %v", id, e)
		}
		if e = writeAtomic(taskFile(a.boardDir(idn.ID), id), data, 0600); e != nil {
			return fail("OPERATION_FAILED", "update task %d: %v", id, e)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"task": updated}, fmt.Sprintf("Updated task %d: %s", updated.ID, updated.Title), nil
}

func (a *application) taskShow(ids []int) (any, string, error) {
	idn, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	if _, err = readBoard(a.boardFile(idn.ID)); err != nil {
		return nil, "", err
	}
	ts := []ticket{}
	var h strings.Builder
	for _, id := range ids {
		t, e := readTicket(a.boardDir(idn.ID), id)
		if e != nil {
			return nil, "", e
		}
		ts = append(ts, t)
		fmt.Fprintf(&h, "Task %d: %s\nStatus: %s\nCreated: %s\nUpdated: %s\n", t.ID, t.Title, t.Status, t.CreatedAt, t.UpdatedAt)
		if t.Body != "" {
			fmt.Fprintf(&h, "\n%s", t.Body)
			if !strings.HasSuffix(t.Body, "\n") {
				h.WriteByte('\n')
			}
		}
		if id != ids[len(ids)-1] {
			h.WriteByte('\n')
		}
	}
	return map[string]any{"tasks": ts}, h.String(), nil
}

func (a *application) taskStatus(ids []int, statusInput string) (any, string, error) {
	status, ok := normalizeStatus(statusInput)
	if !ok {
		return nil, "", fail("INVALID_STATUS", "invalid status %q; valid statuses: %s", statusInput, strings.Join(statuses, ", "))
	}
	idn, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	updated := []ticket{}
	err = a.withLock(func() error {
		if _, e := readBoard(a.boardFile(idn.ID)); e != nil {
			return e
		}
		dir := a.boardDir(idn.ID)
		for _, id := range ids {
			t, e := readTicket(dir, id)
			if e != nil {
				return e
			}
			updated = append(updated, t)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		staged := map[int][]byte{}
		originals := map[int][]byte{}
		for i := range updated {
			originals[updated[i].ID], _ = os.ReadFile(taskFile(dir, updated[i].ID))
			updated[i].Status = status
			updated[i].UpdatedAt = now
			staged[updated[i].ID], _ = encodeTicket(updated[i])
		}
		written := make([]int, 0, len(updated))
		for _, t := range updated {
			if e := writeAtomic(taskFile(dir, t.ID), staged[t.ID], 0600); e != nil {
				for _, id := range written {
					_ = writeAtomic(taskFile(dir, id), originals[id], 0600)
				}
				return fail("OPERATION_FAILED", "update task %d: %v", t.ID, e)
			}
			written = append(written, t.ID)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"tasks": updated, "status": status}, fmt.Sprintf("Updated %d task(s) to %s", len(updated), status), nil
}

func (a *application) archive(args []string) (any, string, error) {
	yes := false
	if len(args) == 1 && args[0] == "--yes" {
		yes = true
	} else if len(args) > 0 {
		return nil, "", usage("usage: kanban archive [--yes] [--json]")
	}
	idn, err := resolveIdentity()
	if err != nil {
		return nil, "", err
	}
	if _, err = readBoard(a.boardFile(idn.ID)); err != nil {
		return nil, "", err
	}
	if !yes {
		f, ok := a.in.(*os.File)
		if !ok || !isTerminal(f) {
			return nil, "", fail("CONFIRMATION_REQUIRED", "archive requires interactive confirmation or --yes")
		}
		fmt.Fprint(a.out, "Archive this board? [y/N] ")
		line, _ := bufio.NewReader(a.in).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes" {
			return map[string]any{"archived": false}, "Archive cancelled", nil
		}
	}
	dest := ""
	err = a.withLock(func() error {
		b, e := readBoard(a.boardFile(idn.ID))
		if e != nil {
			return e
		}
		if _, e = removeMatchingAttachment(idn, a.boardDir(idn.ID)); e != nil {
			return e
		}
		oldAttachment := b.AttachmentPath
		b.AttachmentPath = ""
		if e = writeBoard(a.boardFile(idn.ID), b); e != nil {
			return fail("OPERATION_FAILED", "update board before archive: %v", e)
		}
		stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
		dest = filepath.Join(a.home, "archives", idn.ID, stamp)
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			b.AttachmentPath = oldAttachment
			_ = writeBoard(a.boardFile(idn.ID), b)
			_ = createAttachment(idn, a.boardDir(idn.ID))
			return fail("OPERATION_FAILED", "create archive directory: %v", e)
		}
		if e = os.Rename(a.boardDir(idn.ID), dest); e != nil {
			b.AttachmentPath = oldAttachment
			_ = writeBoard(a.boardFile(idn.ID), b)
			_ = createAttachment(idn, a.boardDir(idn.ID))
			return fail("OPERATION_FAILED", "archive board: %v", e)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"archived": true, "path": dest}, "Archived board to " + dest, nil
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && (st.Mode()&os.ModeCharDevice) != 0
}
func shortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (a *application) help(args []string) (any, string, error) {
	if len(args) > 1 {
		return nil, "", usage("usage: kanban help [COMMAND] [--json]")
	}
	topic := ""
	if len(args) == 1 {
		topic = args[0]
	}
	docs := helpText(topic)
	if docs == "" {
		return nil, "", usage("unknown help topic %q", topic)
	}
	return map[string]any{"topic": topic, "documentation": docs}, docs, nil
}

func helpText(topic string) string {
	all := `kanban — local Markdown kanban boards for agents

Usage:
  kanban init "BOARD NAME" [--json]
  kanban status [--json]
  kanban attach [--json]
  kanban detach [--json]
  kanban task create "TITLE" [--json]
  kanban task --id ID edit --title "TITLE" [--json]
  kanban task --id ID [--id ID ...] show [--json]
  kanban task --id ID [--id ID ...] status STATUS [--json]
  kanban archive [--yes] [--json]
  kanban help [COMMAND] [--json]

Statuses: TODO, BLOCKED, IN_PROGRESS, IN_REVIEW, DONE

Boards are canonical under KANBAN_HOME (or the platform data directory).
The .kanban path is only a symlink to that source of truth.
Git worktrees in the same project share one board.
All commands support --json and errors use nonzero exit codes.
Run kanban help COMMAND for focused documentation.
`
	if topic == "" {
		return all
	}
	m := map[string]string{"init": "kanban init \"BOARD NAME\"\n  Create and automatically attach a board for the current Git project or directory.\n  The required name is immutable. Archive an existing board before initializing another.\n", "status": "kanban status\n  Show board metadata, attachment state, status counts, and every active ticket.\n  No board is a successful result with initialization guidance.\n", "attach": "kanban attach\n  Safely create .kanban as a symlink to the canonical active board.\n", "detach": "kanban detach\n  Remove only a .kanban symlink matching the current board. Safe and idempotent.\n", "task": "kanban task create \"TITLE\"\nkanban task --id ID edit --title \"TITLE\"\nkanban task --id ID [--id ID ...] show\nkanban task --id ID [--id ID ...] status STATUS\n  The CLI never accepts ticket bodies; use a file-editing tool on the canonical Markdown file instead.\n  IDs are sequential. Status changes validate every ID before writing.\n  Status input is case-insensitive. Valid: TODO, BLOCKED, IN_PROGRESS, IN_REVIEW, DONE.\n", "archive": "kanban archive [--yes]\n  Detach and move the active board to timestamped canonical archive storage.\n  Confirmation is required unless --yes is supplied.\n", "help": "kanban help [COMMAND]\n  Show complete or focused agent-oriented command documentation.\n"}
	return m[topic]
}

package herdr

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// §3.6 Stopping a workspace that has linked worktree workspaces beside it ends
// the group, because herdr offers no close that takes the parent alone.
//
// herdr refuses the narrow close with workspace_group_close_required and names
// the flag, on 0.8.2 and 0.9.0 alike (measured 2026-09-10). Before this the
// refusal reached the caller as an unexpected error and the session stayed up:
// a stop that did not stop.
func TestKillClosesAWorktreeGroup(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	repo := gitRepo(t)
	parent, err := b.Create(ctx, backend.CreateSpec{Name: "parent", Dir: repo})
	if err != nil {
		t.Fatalf("creating the parent workspace: %v", err)
	}

	// A worktree workspace is herdr's own shape, made through its verb rather
	// than through Olympus: Olympus has no worktree vocabulary, and the point
	// is what happens when a server ALREADY carries one.
	// --path keeps the checkout inside this case's own directory. Without it
	// herdr puts it under the operator's ~/.herdr, and a test does not write
	// there.
	raw(t, b, "worktree", "create", "--workspace", parent.ID,
		"--branch", "feat", "--path", filepath.Join(shortDir(t), "wt"))

	before, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing before the stop: %v", err)
	}
	if len(before) < 2 {
		t.Fatalf("the worktree workspace was not created, so this proves nothing: %+v", before)
	}

	if err := b.Kill(ctx, parent.ID); err != nil {
		t.Fatalf("stopping a workspace that has a worktree beside it: %v", err)
	}

	after, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing after the stop: %v", err)
	}
	// The whole group, not the parent alone: herdr closes them together or not
	// at all, so a listing that still carries the worktree means the stop was
	// only half done.
	if len(after) != 0 {
		t.Errorf("stopping the parent left %d workspaces standing: %+v", len(after), after)
	}
}

// A workspace with nothing linked to it still closes without the wider flag,
// so the retry does not become the ordinary path.
func TestKillClosesAPlainWorkspace(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s, err := b.Create(ctx, backend.CreateSpec{Name: "plain", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := b.Kill(ctx, s.ID); err != nil {
		t.Fatalf("stopping a plain workspace: %v", err)
	}
	after, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing after the stop: %v", err)
	}
	for _, row := range after {
		if row.ID == s.ID {
			t.Errorf("the workspace told to stop is still listed: %+v", after)
		}
	}
}

// gitRepo makes a repository with one commit, which is the least a worktree
// can be branched from.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := shortDir(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	return dir
}

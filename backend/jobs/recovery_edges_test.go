package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hyskr/easyread/backend/store"
	tr "github.com/hyskr/easyread/backend/translate"
)

func persistFields(ws *store.Workspace, fields Object) error {
	return ws.Update("job", func(j Object) error {
		for k, v := range fields {
			j[k] = v
		}
		return nil
	})
}
func noCancellationHandles(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cancels) != 0 {
		t.Fatal("active cancellation handles leaked")
	}
}

// Original test_delete_during_preparation_keeps_existing_and_new_jobs_running:
// only preparation is replaced; production run/error/finalization paths execute.
func TestDeleteDuringPreparationKeepsExistingAndNewJobsRunning(t *testing.T) {
	lib, first, cfg := fixture(t, 1)
	second := paperIn(t, lib, "paper002")
	if e := first.Write("paper", Object{"meta": Object{}, "blocks": []any{}}); e != nil {
		t.Fatal(e)
	}
	m := newManager(lib, cfg)
	defer m.Close()
	thirdReady := make(chan *store.Workspace, 1)
	m.preparePDF = func(ctx context.Context, w *store.Workspace) ([]map[string]any, error) {
		m.Cancel(w.ID())
		if _, e := lib.Trash(w.ID()); e != nil {
			return nil, e
		}
		third := paperIn(t, lib, "paper003")
		if _, e := m.Prepare(third); e != nil {
			return nil, e
		}
		thirdReady <- third
		return []map[string]any{{"n": 1}}, nil
	}
	if _, e := m.Prepare(first); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Prepare(second); e != nil {
		t.Fatal(e)
	}
	m.startWorkers(1)
	waitState(t, second, "done")
	third := receive(t, thirdReady)
	waitState(t, third, "done")
	m.Close()
	if third == nil || paperState(t, third) != "done" {
		t.Fatal("new paper not completed after deletion")
	}
	if _, e := os.Stat(first.Root); !os.IsNotExist(e) {
		t.Fatalf("deleted paper recreated: %v", e)
	}
	noCancellationHandles(t, m)
}

// Original translation deletion cases, both with and without an engine usage
// record. Only the engine call is fake; cancellation, usage flush and worker
// recovery are the production code paths.
func TestDeleteDuringTranslationAndUsageFlushKeepsQueueRunning(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		name := "without_usage"
		if withUsage {
			name = "with_usage"
		}
		t.Run(name, func(t *testing.T) {
			lib, first, cfg := fixture(t, 1)
			second := paperIn(t, lib, "paper002")
			m := newManager(lib, cfg)
			old := tr.Run
			defer func() { m.Close(); tr.Run = old }()
			thirdReady := make(chan *store.Workspace, 1)
			tr.Run = func(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
				m.Cancel(first.ID())
				if _, e := lib.Trash(first.ID()); e != nil {
					return "", nil, e
				}
				third := paperIn(t, lib, "paper003")
				if _, e := m.Prepare(third); e != nil {
					return "", nil, e
				}
				thirdReady <- third
				if ctx.Err() == nil {
					return "", nil, errors.New("task was not cancelled")
				}
				if withUsage {
					return "", Object{"calls": 1, "input_tokens": 10, "output_tokens": 5}, errors.New("model interrupted")
				}
				return "", nil, ctx.Err()
			}
			enqueue(t, m, first)
			if _, e := m.Prepare(second); e != nil {
				t.Fatal(e)
			}
			m.startWorkers(1)
			waitState(t, second, "done")
			third := receive(t, thirdReady)
			waitState(t, third, "done")
			m.Close()
			if third == nil || paperState(t, third) != "done" {
				t.Fatal("new job stopped with deleted paper")
			}
			if _, e := os.Stat(first.Root); !os.IsNotExist(e) {
				t.Fatalf("deleted paper recreated: %v", e)
			}
			noCancellationHandles(t, m)
		})
	}
}

func TestDeleteDuringRunningStateWriteKeepsQueueRunning(t *testing.T) {
	lib, first, cfg := fixture(t, 1)
	second := paperIn(t, lib, "paper002")
	m := newManager(lib, cfg)
	defer m.Close()
	var removed atomic.Bool
	m.writeStatus = func(w *store.Workspace, fields Object) error {
		if w.ID() == first.ID() && fields["state"] == "running" {
			m.cancels[w.ID()].cancel()
			if _, e := lib.Trash(w.ID()); e != nil {
				return e
			}
			removed.Store(true)
		}
		return persistFields(w, fields)
	}
	if _, e := m.Prepare(first); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Prepare(second); e != nil {
		t.Fatal(e)
	}
	m.startWorkers(1)
	waitState(t, second, "done")
	m.Close()
	if !removed.Load() {
		t.Fatal("running write was not exercised")
	}
	if _, e := os.Stat(first.Root); !os.IsNotExist(e) {
		t.Fatalf("deleted workspace recreated: %v", e)
	}
	noCancellationHandles(t, m)
}

// Ports running/cancelled/error status-write failures. done is an additional
// finalization edge. Permission errors are injected at the exact persistence
// boundary and cannot terminate the worker or strand the next paper.
func TestStatusWriteFailuresDoNotKillWorker(t *testing.T) {
	for _, state := range []string{"running", "cancelled", "error", "done"} {
		t.Run(state, func(t *testing.T) {
			lib, first, cfg := fixture(t, 1)
			second := paperIn(t, lib, "paper002")
			m := newManager(lib, cfg)
			defer m.Close()
			var failures atomic.Int32
			m.writeStatus = func(w *store.Workspace, fields Object) error {
				if w.ID() == first.ID() && fields["state"] == state {
					failures.Add(1)
					return &os.PathError{Op: "write", Path: w.Root + "/job.json", Err: os.ErrPermission}
				}
				return persistFields(w, fields)
			}
			m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
				if w.ID() == first.ID() {
					if state == "cancelled" {
						m.Cancel(w.ID())
						return ctx.Err()
					}
					if state == "error" {
						return errors.New("invalid model response")
					}
				}
				return m.write(w, Object{"state": "done"})
			}
			enqueue(t, m, first)
			enqueue(t, m, second)
			m.startWorkers(1)
			waitState(t, second, "done")
			m.Close()
			if failures.Load() != 1 {
				t.Fatalf("status %s error was not exercised exactly once: %d", state, failures.Load())
			}
			logBytes, e := os.ReadFile(first.Root + "/job.log")
			if e != nil || !strings.Contains(string(logBytes), "permission denied") {
				t.Fatalf("status persistence failure not journaled: %s %v", logBytes, e)
			}
			noCancellationHandles(t, m)
		})
	}
}

func TestExistingWorkspaceWriteErrorsAreNotSilenced(t *testing.T) {
	for _, err := range []error{os.ErrNotExist, os.ErrPermission} {
		lib, w, cfg := fixture(t, 1)
		m := newManager(lib, cfg)
		m.writeStatus = func(*store.Workspace, Object) error {
			return &os.PathError{Op: "write", Path: w.Root + "/temporary", Err: err}
		}
		if e := m.write(w, Object{"state": "cancelled"}); !errors.Is(e, err) {
			t.Fatalf("write error swallowed: %v", e)
		}
		m.Close()
	}
}

func TestRunnerPanicDoesNotKillWorker(t *testing.T) {
	lib, first, cfg := fixture(t, 1)
	second := paperIn(t, lib, "paper002")
	m := newManager(lib, cfg)
	defer m.Close()
	m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
		if w.ID() == first.ID() {
			panic("test runner panic")
		}
		return m.write(w, Object{"state": "done"})
	}
	enqueue(t, m, first)
	enqueue(t, m, second)
	m.startWorkers(1)
	waitState(t, second, "done")
	m.Close()
	waitState(t, first, "error")
	noCancellationHandles(t, m)
}

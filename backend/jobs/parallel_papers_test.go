package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyskr/easyread/backend/store"
)

func paperIn(t *testing.T, lib *store.Library, name string) *store.Workspace {
	t.Helper()
	root := filepath.Join(lib.Root, name)
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	w := store.NewWorkspace(root)
	if e := w.Write("paper", Object{"meta": Object{"pages": []any{Object{"n": 1}}, "page_count": 1}, "blocks": []any{}, "translation": Object{}}); e != nil {
		t.Fatal(e)
	}
	return w
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach synchronization point")
		var zero T
		return zero
	}
}
func waitSignal(t *testing.T, ch <-chan struct{}) { t.Helper(); receive(t, ch) }
func enqueue(t *testing.T, m *Manager, w *store.Workspace) {
	t.Helper()
	if _, e := m.Enqueue(w, Object{"pages": []int{1}}); e != nil {
		t.Fatal(e)
	}
}
func paperState(t *testing.T, w *store.Workspace) string {
	t.Helper()
	j, e := w.Load("job")
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprint(j["state"])
}

// Port of test_config_is_bounded_and_starts_the_requested_workers. Each
// requested worker must reach a barrier; extra papers stay durably queued.
func TestPaperWorkerCountBoundsAndStartsRequestedWorkers(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{{"missing", nil, 1}, {"one", 1, 1}, {"two", 2, 2}, {"four", 4, 4}, {"eight", 8, 4}, {"hundred", 100, 4}, {"zero", 0, 1}, {"negative", -1, 1}, {"string", "8", 1}, {"boolean", true, 1}, {"fractional", 2.5, 1}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lib, first, cfg := fixture(t, 1)
			if tc.value != nil {
				if _, e := cfg.Save(Object{"paper_concurrency": tc.value}); e != nil {
					t.Fatal(e)
				}
			}
			loaded, e := cfg.Load()
			if e != nil {
				t.Fatal(e)
			}
			if got := workerCount(loaded); got != tc.want {
				t.Fatalf("workerCount(%v)=%d, want %d", tc.value, got, tc.want)
			}
			m := newManager(lib, cfg)
			defer m.Close()
			started := make(chan string, 6)
			var active, peak atomic.Int32
			m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
				now := active.Add(1)
				defer active.Add(-1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				started <- w.ID()
				<-ctx.Done()
				return ctx.Err()
			}
			papers := []*store.Workspace{first}
			for n := 1; n < 6; n++ {
				papers = append(papers, paperIn(t, lib, fmt.Sprintf("paper%03dextra", n)))
			}
			for _, w := range papers {
				enqueue(t, m, w)
			}
			m.startWorkers(workerCount(loaded))
			running := map[string]bool{}
			for n := 0; n < tc.want; n++ {
				running[receive(t, started)] = true
			}
			for _, w := range papers {
				want := "queued"
				if running[w.ID()] {
					want = "running"
				}
				if got := paperState(t, w); got != want {
					t.Fatalf("%s state=%s, want %s", w.ID(), got, want)
				}
			}
			if peak.Load() != int32(tc.want) {
				t.Fatal(peak.Load())
			}
		})
	}
}

// Port of test_paper_concurrency_persists_in_existing_config.
func TestPaperConcurrencyPersistsInExistingConfig(t *testing.T) {
	_, _, cfg := fixture(t, 1)
	if _, e := cfg.Save(Object{"paper_concurrency": 3}); e != nil {
		t.Fatal(e)
	}
	loaded, e := cfg.Load()
	if e != nil || workerCount(loaded) != 3 {
		t.Fatal(loaded, e)
	}
}

// Port of test_two_papers_run_together_and_third_waits, with barriers rather
// than sleeps. A third paper cannot enter the runner before one slot exits.
func TestTwoPapersRunTogetherAndThirdWaits(t *testing.T) {
	lib, first, cfg := fixture(t, 1)
	papers := []*store.Workspace{first, paperIn(t, lib, "paper002"), paperIn(t, lib, "paper003")}
	m := newManager(lib, cfg)
	defer m.Close()
	started := make(chan string, 3)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var active, peak atomic.Int32
	m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- w.ID()
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return m.write(w, Object{"state": "done"})
	}
	for _, w := range papers {
		enqueue(t, m, w)
	}
	m.startWorkers(2)
	a, b := receive(t, started), receive(t, started)
	if a == b || a == papers[2].ID() || b == papers[2].ID() {
		t.Fatal(a, b)
	}
	if paperState(t, papers[2]) != "queued" {
		t.Fatal("third did not wait")
	}
	once.Do(func() { close(release) })
	if receive(t, started) != papers[2].ID() {
		t.Fatal("third paper did not advance")
	}
	for _, w := range papers {
		waitState(t, w, "done")
	}
	m.Close()
	if peak.Load() != 2 {
		t.Fatal(peak.Load())
	}
}

// Port of test_finishing_old_task_preserves_requeued_tasks_cancel_event.
// The old runner has published done but is still unwinding when the new run
// starts. Cancel must reach the new run, even after the old cleanup finishes.
func TestFinishingOldTaskPreservesRequeuedCancelHandle(t *testing.T) {
	lib, w, cfg := fixture(t, 1)
	m := newManager(lib, cfg)
	defer m.Close()
	oldPublished, newStarted := make(chan struct{}), make(chan context.Context, 1)
	oldRelease := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(oldRelease) })
	var calls atomic.Int32
	m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
		if calls.Add(1) == 1 {
			if e := m.write(w, Object{"state": "done"}); e != nil {
				return e
			}
			close(oldPublished)
			<-oldRelease
			return nil
		}
		newStarted <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	enqueue(t, m, w)
	oldExited := make(chan struct{})
	go func() { defer close(oldExited); m.process(w.ID()) }()
	waitSignal(t, oldPublished)
	enqueue(t, m, w)
	newExited := make(chan struct{})
	go func() { defer close(newExited); m.process(w.ID()) }()
	ctx := receive(t, newStarted)
	m.mu.Lock()
	newHandle := m.cancels[w.ID()]
	m.mu.Unlock()
	once.Do(func() { close(oldRelease) })
	waitSignal(t, oldExited)
	m.mu.Lock()
	after := m.cancels[w.ID()]
	m.mu.Unlock()
	if after == nil || after != newHandle {
		t.Fatal("old cleanup deleted new cancellation handle")
	}
	m.Cancel(w.ID())
	waitSignal(t, ctx.Done())
	waitSignal(t, newExited)
	waitState(t, w, "cancelled")
	m.mu.Lock()
	left := len(m.cancels)
	m.mu.Unlock()
	if left != 0 {
		t.Fatal("cancel handle leaked")
	}
}

func TestOldRunFailureDoesNotOverwriteRequeuedRun(t *testing.T) {
	lib, w, cfg := fixture(t, 1)
	m := newManager(lib, cfg)
	defer m.Close()
	oldPublished, newStarted := make(chan struct{}), make(chan struct{})
	releaseOld, releaseNew := make(chan struct{}), make(chan struct{})
	var oldOnce, newOnce sync.Once
	defer oldOnce.Do(func() { close(releaseOld) })
	defer newOnce.Do(func() { close(releaseNew) })
	var calls atomic.Int32
	m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
		if calls.Add(1) == 1 {
			if e := m.write(w, Object{"state": "done"}); e != nil {
				return e
			}
			close(oldPublished)
			<-releaseOld
			return errors.New("late error in old run")
		}
		close(newStarted)
		select {
		case <-releaseNew:
			return m.write(w, Object{"state": "done"})
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	enqueue(t, m, w)
	oldExit := make(chan struct{})
	go func() { defer close(oldExit); m.process(w.ID()) }()
	waitSignal(t, oldPublished)
	enqueue(t, m, w)
	newExit := make(chan struct{})
	go func() { defer close(newExit); m.process(w.ID()) }()
	waitSignal(t, newStarted)
	oldOnce.Do(func() { close(releaseOld) })
	waitSignal(t, oldExit)
	if paperState(t, w) != "running" {
		t.Fatal("old failure overwrote new run")
	}
	newOnce.Do(func() { close(releaseNew) })
	waitSignal(t, newExit)
	waitState(t, w, "done")
}

func TestCancellationFreesOneSlotWithoutCancellingOtherPapers(t *testing.T) {
	lib, first, cfg := fixture(t, 1)
	papers := []*store.Workspace{first, paperIn(t, lib, "paper002"), paperIn(t, lib, "paper003")}
	m := newManager(lib, cfg)
	defer m.Close()
	type startedRun struct {
		id  string
		ctx context.Context
	}
	started := make(chan startedRun, 3)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	m.runBulk = func(ctx context.Context, w *store.Workspace, j Object) error {
		started <- startedRun{w.ID(), ctx}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return m.write(w, Object{"state": "done"})
		}
	}
	for _, w := range papers {
		enqueue(t, m, w)
	}
	m.startWorkers(2)
	a, b := receive(t, started), receive(t, started)
	if _, e := m.Enqueue(first, Object{}); e == nil {
		t.Fatal("duplicate live task accepted")
	}
	m.Cancel(a.id)
	waitSignal(t, a.ctx.Done())
	third := receive(t, started)
	if third.id != papers[2].ID() {
		t.Fatal(third.id)
	}
	if b.ctx.Err() != nil || third.ctx.Err() != nil {
		t.Fatal("cancellation leaked to another paper")
	}
	if !m.Busy() {
		t.Fatal("running work reported idle")
	}
	once.Do(func() { close(release) })
	for _, w := range papers {
		if w.ID() == a.id {
			waitState(t, w, "cancelled")
		} else {
			waitState(t, w, "done")
		}
	}
	m.Close()
	if m.Busy() {
		t.Fatal("completed manager reported busy")
	}
}

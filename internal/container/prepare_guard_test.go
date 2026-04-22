package container

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestPrepareGuardRunsOnce(t *testing.T) {
	var g PrepareGuard
	var calls atomic.Int64
	fn := func() error { calls.Add(1); return nil }

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Do(fn) }()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("PrepareGuard.Do ran fn %d times, want 1", got)
	}
}

func TestKeyedPrepareGuardDedupesPerKey(t *testing.T) {
	var g KeyedPrepareGuard
	var calls sync.Map // key → *atomic.Int64

	fn := func(key string) func() error {
		return func() error {
			v, _ := calls.LoadOrStore(key, &atomic.Int64{})
			v.(*atomic.Int64).Add(1)
			return nil
		}
	}

	var wg sync.WaitGroup
	const perKey = 8
	keys := []string{"alpha", "beta", "alpha", "gamma", "beta"} // 3 distinct keys
	for _, k := range keys {
		for i := 0; i < perKey; i++ {
			wg.Add(1)
			go func(key string) { defer wg.Done(); _ = g.Do(key, fn(key)) }(k)
		}
	}
	wg.Wait()

	for _, key := range []string{"alpha", "beta", "gamma"} {
		v, ok := calls.Load(key)
		if !ok {
			t.Errorf("key %q: fn never ran", key)
			continue
		}
		if got := v.(*atomic.Int64).Load(); got != 1 {
			t.Errorf("key %q: fn ran %d times, want 1", key, got)
		}
	}
}

func TestPrepareGuardCachesError(t *testing.T) {
	var g PrepareGuard
	sentinel := errPrepareGuard("boom")
	var calls atomic.Int64
	fn := func() error { calls.Add(1); return sentinel }

	for i := 0; i < 3; i++ {
		if err := g.Do(fn); err != sentinel {
			t.Errorf("call %d: err = %v, want %v", i, err, sentinel)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fn ran %d times, want 1 (error must be cached)", got)
	}
}

type errPrepareGuard string

func (e errPrepareGuard) Error() string { return string(e) }

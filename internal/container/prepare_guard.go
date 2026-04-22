package container

import "sync"

// PrepareGuard dedupes Prepare() side effects (docker build, docker cp,
// precheck) across pool workers that share a Container via Clone(). The
// zero value is ready to use; a single PrepareGuard is attached to the
// shared original by the container factory, and each Clone() copies the
// pointer so all clones hit the same sync.Once.
type PrepareGuard struct {
	once sync.Once
	err  error
}

// Do runs fn at most once across all callers sharing this guard, and
// returns the cached error on subsequent calls.
func (p *PrepareGuard) Do(fn func() error) error {
	p.once.Do(func() { p.err = fn() })
	return p.err
}

// KeyedPrepareGuard dedupes Prepare() per key across clones. Use it when
// the work to dedupe depends on a value only known at Prepare time (e.g.
// docker-exec resolves ContainerName from a {{ROLE}} template per run —
// different roles need independent prepare runs, same roles share).
type KeyedPrepareGuard struct {
	mu sync.Mutex
	m  map[string]*PrepareGuard
}

// Do runs fn at most once per key across all callers sharing this guard.
func (k *KeyedPrepareGuard) Do(key string, fn func() error) error {
	k.mu.Lock()
	if k.m == nil {
		k.m = make(map[string]*PrepareGuard)
	}
	g, ok := k.m[key]
	if !ok {
		g = &PrepareGuard{}
		k.m[key] = g
	}
	k.mu.Unlock()
	return g.Do(fn)
}

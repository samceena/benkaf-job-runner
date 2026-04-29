# Mutual Exclusion Lock

## What is a mutual exclusion lock

A mutex (mutual exclusion lock) is a synchronization primitive that guarantees only one goroutine can be inside a given critical section at a time. It's the basic tool for protecting shared, mutable state from concurrent modification.

## Why do we need it (Golang)

Go maps are **not** safe for concurrent use. If two goroutines write to the same map at the same time, the runtime detects it and panics with `fatal error: concurrent map writes`. Even one writer racing with one reader is undefined behavior.

The race detector (`go test -race ./...`) instruments memory accesses and flags unprotected concurrent reads/writes. It's how you'd catch a missing mutex during code review or CI.

## The pattern

```go
var (
    mu   sync.Mutex
    data = make(map[string]string)
)

func write(k, v string) {
    mu.Lock()
    defer mu.Unlock()
    data[k] = v
}
```

Two important conventions:

1. **Acquire immediately, defer the unlock.** `Lock()` happens now; `Unlock()` is deferred so it runs when the function returns — including on panics and early returns. Without `defer`, a panic or a forgotten branch would leave the mutex held forever and deadlock every future caller.
2. **Lock around the smallest necessary section.** Holding the mutex longer than needed serializes goroutines that could otherwise run in parallel.

## Real example from this project

```go
func (m *MemoryStore) CreateJob(ctx context.Context, j *job.Job) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if _, exists := m.jobs[j.ID]; exists {
        return fmt.Errorf("job %s already exists", j.ID)
    }
    copied := *j
    m.jobs[j.ID] = &copied
    return nil
}
```

**What this protects against, precisely:** the actors are *two goroutines inside the controller process*, not two workers. When a client POSTs to `/jobs`, `net/http` spawns a goroutine per request. With many concurrent requests, multiple goroutines call `CreateJob` at the same time. Without the mutex, two things can go wrong:

1. **Map panic.** `m.jobs[j.ID] = &copied` is a write. Two concurrent writes to the same map crash the program.
2. **Check-then-act race.** Two goroutines could both pass the `if _, exists` check before either inserts, then both insert. The mutex makes the check-and-insert atomic.

(Note: in the current project, IDs are randomly generated, so collisions on existence are vanishingly rare. The dominant reason the mutex exists is the map-panic protection. The check-then-act race becomes important when IDs are user-supplied — e.g., `RegisterWorkers`, where worker IDs come from clients.)

## What this lock does *not* do

The mutex is **in-process**. It only synchronizes goroutines inside the controller. It does nothing to prevent:

- Two **workers** (separate processes, possibly on separate machines) from both claiming the same job.
- A **second controller** running somewhere from racing the first.

That's the whole reason this project needs a **distributed lock** in Phase 3. The state machine (`PENDING → CLAIMED → RUNNING`) plus `WorkerID` plus a fencing token gives mutual exclusion *across the network*. Same goal as `sync.Mutex`, different mechanism, different failure modes (network partitions, GC pauses, clock skew). See `docs/distributed-locks.md`.

## Race conditions vs deadlocks

Worth keeping these straight — they're often confused:

- **Race conditions** are what locks *prevent*. They happen when there's *no* synchronization and the result depends on goroutine scheduling.
- **Deadlocks** are what *bad lock usage* can cause. Two goroutines each holding one lock and waiting on the other will wait forever. You need locks to exist before you can deadlock.

So locks fix one problem and introduce the *possibility* of another. The cure for deadlocks is consistent lock ordering, not removing the locks.

## Golang concurrency table

```
  ┌────────────────────┬─────────────────────────────┬───────────────────┐
  │     Primitive      │          Use when           │       Cost        │
  ├────────────────────┼─────────────────────────────┼───────────────────┤
  │ atomic.Int64 etc.  │ Single integer/pointer      │ Lockless,         │
  │                    │ value                       │ cheapest          │
  ├────────────────────┼─────────────────────────────┼───────────────────┤
  │ sync.Mutex         │ Multi-step critical         │ One goroutine at  │
  │                    │ section, exclusive access   │ a time            │
  ├────────────────────┼─────────────────────────────┼───────────────────┤
  │ sync.RWMutex       │ Read-heavy, occasional      │ Many readers OR   │
  │                    │ writes                      │ one writer        │
  ├────────────────────┼─────────────────────────────┼───────────────────┤
  │ Channels           │ Coordinating ownership      │ Go's preferred    │
  │                    │ transfer between goroutines │ idiom             │
  ├────────────────────┼─────────────────────────────┼───────────────────┤
  │ Distributed lock   │ Mutual exclusion across     │ Network           │
  │ (lease + fencing)  │ processes/machines          │ round-trips,      │
  │                    │                             │ consensus         │
  └────────────────────┴─────────────────────────────┴───────────────────┘
```

Mutex sits in the middle: more powerful than atomics (multi-step operations are atomic), less powerful than a distributed lock (can't cross process boundaries).

## Granularity

`MemoryStore` uses **one mutex for the entire store**. Every operation — `CreateJob`, `GetJob`, `UpdateJob`, `ListJobsByState` — serializes through the same lock. That's coarse-grained locking. It's fine for an in-memory toy under low load, but at scale it becomes a bottleneck.

Finer-grained alternatives (for later):

- **Per-key locks** (sharded map of mutexes keyed by job ID) — concurrent operations on different jobs proceed in parallel.
- **`sync.RWMutex`** — many concurrent readers, exclusive writers.
- **`sync.Map`** — built-in concurrent map for specific access patterns (write-once, read-many).
- **Lock-free** data structures via atomics and immutable snapshots.

## TL;DR

Mutex = in-process mutual exclusion via shared memory. Cheap, fast, single-machine. Always pair `Lock()` with `defer Unlock()`. The instant the problem crosses process boundaries (two workers, two controllers, anything on the wire), `sync.Mutex` is no longer the right tool — that's where distributed locks come in.

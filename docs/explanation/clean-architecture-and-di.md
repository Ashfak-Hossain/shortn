# Clean architecture and dependency injection

This is the conceptual companion to [ARCHITECTURE.md](../../ARCHITECTURE.md). That
document maps the system; this one explains the one mechanism that keeps the map
stable as the system grows: the domain depends on interfaces it defines, concrete I/O
types satisfy those interfaces from other packages, and exactly one function knows
which concrete is which. The result is that swapping an implementation — Postgres for a
fake, a random ID generator for Snowflake, a bare store for a cache-wrapped one — is a
local edit, never a ripple through the codebase.

The layering decisions behind this live in the ADRs; this doc adds the depth a reader
needs to operate or extend the wiring without re-deriving it.

## The invariant

`internal/shortener` is the domain. It imports no `database/sql`, no `pgx`, no
`net/http`. It imports `context`, `errors`, `fmt`, `net`, `net/url`, `strings`, and
`time` — standard library only. This is checkable, and it is the property everything
else rests on: the core business rules (validate a URL, retry on code collision up to
`maxCreateRetries`, resolve a code, return stats) have no idea where data is stored or
how requests arrive. Break this import rule and the rest of the design unravels, so it
is the first thing to defend in review.

The arrow runs one way: `http` → `shortener` → interfaces. Nothing points back inward.

## Go has no `implements`

A type satisfies an interface by having methods whose names and signatures match — the
compiler checks the shape, never a declaration. The consumer declares the interface it
needs; the provider satisfies it without knowing the interface exists. That is why
`internal/shortener` can declare `LinkStore` while `internal/store` (the Postgres
implementation) never imports `shortener` to "implement" it. The store imports the
domain only to use its types (`*shortener.Link`, `shortener.Stats`, the sentinel
errors), and satisfaction falls out for free.

The codebase pins satisfaction down with compile-time assertions in four files:

```go
var _ shortener.LinkStore   = (*Postgres)(nil)            // internal/store/postgres.go:28
var _ shortener.LinkStore   = (*ResilientStore)(nil)      // internal/resilience/resilience.go:39
var _ shortener.LinkStore   = (*CachingStore)(nil)        // internal/cache/store.go:42
var _ shortener.IDGenerator = (*SnowflakeGenerator)(nil)  // internal/idgen/snowflake.go:53
```

Each line reads: fail to compile in *this* file if the type ever stops satisfying the
interface. It binds nothing at runtime — `_` discards a `nil`. It exists so a signature
mismatch surfaces in the implementer's file with a clear message instead of at a distant
call site. It is optional: `Pinger`, `Publisher`, and `IdempotencyStore` carry no such
assertion and are still verified, just at the wiring line in `main` instead.

A type may have more methods than an interface requires; the extras are invisible when
the value is held as that interface. `*store.Postgres` carries `InsertClick`,
`SaveOffset`, and `LoadOffset` on top of the five `LinkStore` methods. The domain never
sees them. The analytics consumer, which holds the concrete `*store.Postgres`, calls
them directly. An interface constrains the view, not the type.

## The interfaces

Five interfaces draw the domain boundary. Each is defined by its consumer and satisfied
by a type in a different package.

| Interface | Defined in | Methods | Satisfied by |
| --- | --- | --- | --- |
| `shortener.LinkStore` | `internal/shortener/shortener.go:44` | `Create`, `GetByCode`, `GetStats`, `List`, `Delete` | `*store.Postgres`, `*resilience.ResilientStore`, `*cache.CachingStore` |
| `shortener.IDGenerator` | `internal/shortener/shortener.go:59` | `Generate` | `*idgen.SnowflakeGenerator` (prod), `idgen.RandomBase62` (tests) |
| `http.Pinger` | `internal/http/router.go:22` | `Ping` | `*pgxpool.Pool` (third-party) |
| `http.Publisher` | `internal/http/router.go:27` | `Publish` | `*events.KafkaPublisher` |
| `http.IdempotencyStore` | `internal/http/router.go:33` | `Get`, `Set` | `*idempotency.Store` |

### LinkStore — three implementers stacked as decorators

`LinkStore` is the interface worth understanding in full, because three types satisfy it
and two of them wrap a third. Each holds a `LinkStore` in a `next` field and is itself a
`LinkStore`, so the layers nest without any of them knowing how deep the stack goes:

```text
shortener.Service ──depends on──► LinkStore
                                      ▲ satisfied by
        *cache.CachingStore ─wraps─► *resilience.ResilientStore ─wraps─► *store.Postgres
        (read-through Redis)        (circuit breaker)                    (the only SQL)
```

`*store.Postgres` is the source of truth and the only place SQL lives.
`*resilience.ResilientStore` wraps it with a `sony/gobreaker` circuit breaker named
`postgres`: after `breakerMaxConsecFails` (5) consecutive failures the breaker opens,
and for the next `breakerCooldown` (5s) every call fails fast with
`shortener.ErrUnavailable` instead of waiting out the timeout and piling up connections.
`ErrNotFound` and `ErrCodeExists` are not counted as failures — they mean Postgres is
healthy and answering. `*cache.CachingStore` wraps that with a Redis read-through cache
(`cacheTTL` = 1 hour). The `Service` calls `s.store.GetByCode(...)` and cannot tell
whether the call is hitting Redis, then the breaker, then Postgres, or just one of
those. All three are `LinkStore`.

This is the decorator pattern, and it works only because satisfaction is implicit. A
fourth layer — metrics, tracing, a second cache tier — could slot in the same way
without touching `Service` or any existing store.

### IDGenerator — two implementers, two receiver styles

`IDGenerator` has a single method, `Generate() (string, error)`.
`*idgen.SnowflakeGenerator` (pointer receiver) is the production implementation;
`idgen.RandomBase62` (value receiver) is the original generator, now referenced only by
tests. The receiver split follows a simple rule: pointer receiver when the type holds a
handle or is otherwise non-trivial, value receiver for tiny immutable config like
`RandomBase62{length}`. The consequence shows in the assertions — a pointer-receiver
type satisfies the interface only as `*SnowflakeGenerator`, which is why the assertion
reads `(*SnowflakeGenerator)(nil)` with the star.

### Pinger — a third-party type satisfies it for free

`Pinger` requires `Ping(ctx context.Context) error`. Nothing in this codebase is written
to satisfy it. `*pgxpool.Pool` from the pgx library already has that exact method, so it
fits, and `main` passes the pool straight into `NewOpsRouter`. The `readyz` handler
calls `h.pinger.Ping(...)` under a 2-second timeout and stays ignorant of Postgres
entirely — if the pool can't ping, it returns `503` and load balancers stop routing.
The pgx author never heard of this interface; that is the point of defining interfaces
on the consumer side.

### Publisher — best-effort, off the request path

`Publisher` requires `Publish(ctx, events.LinkClicked) error`, satisfied by
`*events.KafkaPublisher`. The redirect handler builds a `LinkClicked` event and calls
`h.publisher.Publish(...)` in a goroutine, *after* the redirect is on the wire, on a
context detached from request cancellation but carrying its trace context. A publish
failure is logged and swallowed: the redirect already succeeded and click-tracking is
best-effort. The handler knows Kafka only as this one method.

### IdempotencyStore — dedupe retried creates

`IdempotencyStore` has `Get` and `Set`, satisfied by `*idempotency.Store` (Redis-backed,
24-hour TTL). When a create carries an `Idempotency-Key`, the handler checks `Get` for a
prior code, and after a successful create calls `Set` to remember it, so a retried
request returns the same link instead of minting a second one.

## The composition root

All wiring happens in one function, `main()` in `cmd/api/main.go`. Concrete types are
built bottom-up and handed to constructors as interfaces:

```text
gen          := idgen.NewSnowflakeGenerator(uint16(wid), sq)   →  IDGenerator
st           := store.New(pool)                                →  *Postgres        (a LinkStore)
resilient    := resilience.NewResilientStore(st, logger)       →  wraps it         (a LinkStore)
cachingStore := cache.NewCachingStore(resilient, ...)          →  wraps that       (a LinkStore)
svc          := shortener.NewService(cachingStore, gen)        ←  interfaces injected
pub          := events.NewKafkaPublisher(...)                  →  Publisher
idem         := idempotency.New(rdb, idempotencyTTL)           →  IdempotencyStore
httpapi.NewRouter(httpapi.RouterDeps{ Service: svc, Publisher: pub, Idempotency: idem, ... })
httpapi.NewOpsRouter(pool, logger, ...)                        ←  pool = Pinger
```

`NewRouter` takes a `RouterDeps` struct rather than a long positional list, so the call
site reads as named fields. Only `main` names the concrete types — Postgres, Redis, the
circuit breaker, Kafka, Snowflake. Every other package sees interfaces. To swap any
implementation, edit `main` and nothing else. The store composition (raw → breaker →
cache) is just three constructor calls in this one place; the order is the decorator
order, inside out.

## The honest boundary

Not every call goes through an interface, by design. The analytics consumer
(`cmd/analytics/main.go`) holds the concrete `*store.Postgres` and calls `InsertClick`,
`SaveOffset`, and `LoadOffset` directly, inside a transaction it manages itself for
exactly-once processing. Those methods are not part of any domain interface because the
consumer is not the domain — it is a separate process with its own concerns
(offset tracking, transactional click insertion). Interfaces guard the domain boundary,
not every call in the system. Forcing an interface around the consumer's store access
would buy nothing and obscure the transaction.

## Why it pays

Two changes the system absorbed later show the cost staying flat. The Snowflake
generator replaced `RandomBase62`: `shortener` and `http` referenced only `IDGenerator`, so the
swap was a single edit in `main`. The Redis cache and the circuit breaker each arrived
as a new `LinkStore` decorator wrapped around the existing chain — again, `Service` and
the handlers were untouched, because both new layers are just another `LinkStore`. The
pattern that makes those edits one-line edits is the same one enforced by the import
rule at the top of this doc.

## See also

- [ARCHITECTURE.md](../../ARCHITECTURE.md) — the system map this doc explains.
- [ADR 0009 — Resilience](../architecture/0009-resilience.md) — the circuit-breaker decision.
- [ADR 0006 — Caching strategy](../architecture/0006-caching-strategy.md) — the read-through cache decision.

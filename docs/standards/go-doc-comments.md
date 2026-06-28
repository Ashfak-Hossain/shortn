# Go Doc Comment Standard

Use this when writing or reviewing Go doc comments. It encodes the official
conventions: Effective Go, `go.dev/doc/comment`, and the gofmt doc-comment
format (Go 1.19+). Two modes: **prepare** (write/rewrite comments) and **check**
(audit and report fixes). Produce comments that `go doc` and pkg.go.dev render
correctly and that read like standard-library docs.

---

## Hard rules (always)

1. **Every exported identifier gets a doc comment.** Exported = capitalized
   first letter (funcs, types, methods, consts, vars, fields, the package). No
   exceptions for "obvious" ones — if it's part of the public API, it's documented.
2. **The comment immediately precedes the declaration, with no blank line
   between.** A blank line detaches it and it stops being a doc comment.
3. **Begin with the identifier's name.**
   - Func/method: `// Encode writes ...` (name then a verb, present tense, 3rd person).
   - Type: `// Client is a ...` / `// Reader represents ...`.
   - Package: `// Package shortener implements ...`.
   - Const/var: `// MaxRetries is the ...` / `// ErrNotFound is returned when ...`.
4. **Write complete sentences.** Capitalized first word, ends with a period,
   even one-liners.
5. **Describe contract, not implementation.** Document _what_ it does, what it
   guarantees, and what the caller must know — not _how_ the body works. The how
   goes in ordinary `//` comments inside the body, on the "why" only.
6. **Don't restate the signature.** `// GetByID returns the user with the given
id.` is filler. Say what's non-obvious: error conditions, nil behavior,
   ownership, blocking, allocation.
7. **Use the right voice; never first person.** No `I`/`we`/`us`/`our`/`let's` —
   describe the code, not the team. Doc comments read as declarative third
   person; body comments as imperative. See **Voice and person** below.

---

## Voice and person

Comments describe the code, not the people who wrote it. Match the Go standard
library and the Google/LLVM style guides: declarative doc comments, imperative
body comments, and no first person.

- **No first-person pronouns.** Drop `I`, `we`, `us`, `our`, `let's`; they
  describe the team, not the code, and read as chatty.
  - `// We ping only to surface a warning` → `// Ping only surfaces a warning.`
  - `// We refuse traffic when the DB is down` → `// Refuse traffic when the DB is unreachable.`
- **Doc comments — declarative, third person, present tense.** State what the
  identifier is or does: `// Resolve returns the long URL for code.`
- **Body comments — imperative mood.** Direct, like a commit subject:
  `// Seek each partition to the offset stored in Postgres.`
- **Address the reader sparingly.** "you" is acceptable in package docs that
  explain usage; avoid it in routine body comments.
- **No hedging or filler openers.** Cut `Note that`, `Basically`, `Obviously`,
  `Of course`, `As you can see` — state the point directly.
- **TODOs name an owner:** `// TODO(username): drop once the DLQ exists.`
- **No roadmap or curriculum references.** Describe the code as it is, not where
  it sits in a plan: drop `(next milestone)`, `module 6.4`, `for week 8`, `the v2 rewrite`,
  `coming later`. Genuinely deferred work goes in a `TODO(owner):` that names the
  condition, never the schedule.
- **Plain English over Latin abbreviations.** Avoid `e.g.`, `i.e.`, `etc.` Write
  `for example`, `that is`, or just name the items: `// statuses like open,
  closed, merged` — not `// e.g. open, closed`.

---

## Package comment

- One per package, immediately above the `package` clause, starting
  `// Package name ...`. For multi-file or long package docs, put it alone in
  `doc.go`.
- First sentence is a one-line summary (it appears in package lists). Follow with
  optional paragraphs, usage examples, and headings as needed.

```go
// Package idgen produces short, URL-safe identifiers.
//
// It exposes a single Generator interface; concrete strategies (random base62,
// Snowflake) are selected at construction time and are safe for concurrent use.
package idgen
```

---

## Content checklist for a good comment

Include whichever apply; omit the rest. Reference parameters and fields by name.

- **Errors:** which errors are returned and when. Name sentinel errors
  (`Returns [ErrNotFound] if the code does not exist.`).
- **Nil / zero value:** what a nil receiver, nil arg, or the type's zero value
  means, if it's usable or meaningful.
- **Concurrency:** state it explicitly when relevant — "safe for concurrent use
  by multiple goroutines" or "not safe for concurrent use".
- **Ownership / lifecycle:** who closes it, whether the callee retains a slice,
  whether the caller must not mutate a returned value.
- **Blocking / context:** whether it blocks, respects `ctx` cancellation, or has
  a timeout.
- **Units and ranges:** units for numbers, valid ranges, what out-of-range does.

Don't document unexported identifiers for the public API surface, but do comment
unexported code where intent is non-obvious — those just aren't held to the
"start with the name / full API contract" bar.

---

## gofmt doc-comment format (Go 1.19+)

Doc comments are lightly structured. Use only these constructs so gofmt keeps
them stable and pkg.go.dev renders them.

- **Paragraphs:** runs of unindented text separated by a blank comment line
  (`//`). gofmt reflows them — don't hand-wrap to a column.
- **Headings:** a line beginning with `#` (hash, space).

  ```go
  // # Errors
  ```

- **Lists:**
  - Bullet items start with `-`, `*`, or `+` then a space.
  - Numbered items start with `1.` or `1)` then a space.
  - Separate the list from surrounding paragraphs with blank `//` lines.
- **Code blocks:** indent the lines (one tab past the surrounding text). gofmt
  preserves them verbatim.

  ```go
  // Example:
  //
  // id := g.New()
  // fmt.Println(id)
  ```

- **Doc links** (resolve to symbols, no manual URLs):
  - `[Name]` — symbol in the same package.
  - `[pkg.Name]`, `[pkg.Type.Method]`, `[*pkg.Type]` — other packages.
  - `[Type.Method]` — method on a same-package type.
- **URLs** in plain text are auto-linked; don't wrap them in markdown link syntax.

Do **not** use Markdown beyond the above (no `**bold**`, `_italic_`, backtick
spans, or `[text](url)` links) — godoc does not render it.

---

## Conventions by declaration kind

- **Function/method:** `// Name <verb-phrase>.` Present tense, indicative.
  `// Resolve returns the long URL for code, or [ErrNotFound].`
- **Type:** `// Name is a ...` or `// Name represents ...`. Describe the concept
  and the zero value if meaningful.
- **Interface:** describe the behavior/contract implementers must satisfy, not a
  specific implementation.
- **Struct fields:** comment exported fields that aren't self-explanatory, on the
  line above the field. Group-document tightly related fields in one comment when
  clearer.
- **Grouped const/var (`const (...)`):** a comment above the block documents the
  group; per-item comments document specifics. Sentinel errors each get
  `// ErrX is returned when ...`.
- **Deprecation:** add a paragraph starting exactly `// Deprecated:` (tools and
  linters key on this). State the replacement.

  ```go
  // Deprecated: use [NewClientWithConfig] instead.
  ```

---

## Anti-patterns to flag and fix

- Blank line between comment and declaration (detaches the doc comment).
- Comment that doesn't start with the identifier's name.
- Opening with "This function/type ..." — drop it, start with the name.
- Restating the signature in words ("// Add takes a and b and returns them added").
- Missing period / lowercase start / sentence fragments.
- Markdown link/emphasis syntax, or manual `http://...` links where a `[Symbol]`
  doc link is correct.
- Hand-wrapped paragraphs fighting gofmt's reflow.
- Exported symbol with no doc comment.
- Documenting _how_ (implementation detail) instead of _what/why_ (contract).
- First-person pronouns (`I`, `we`, `us`, `our`, `let's`) — rewrite around the code.
- Hedging or filler openers: `Note that`, `Basically`, `Obviously`, `Of course`, `As you can see`.
- Commented-out code left in the file — delete it; git remembers.
- Roadmap or curriculum references (`(next milestone)`, `module 6.4`, `week 8`, `v2`) — comment the code, not the plan.
- Latin abbreviations (`e.g.`, `i.e.`, `etc.`) — write `for example`, `that is`, or list the items.

---

## Check mode output format

When auditing a file, for each issue report: the identifier, the rule violated
(one of the categories above), and the corrected comment. End with the
rewritten file or a diff. If a comment is already correct, say so rather than
inventing changes. Don't add documentation to unexported helpers unless intent
is genuinely unclear.

---

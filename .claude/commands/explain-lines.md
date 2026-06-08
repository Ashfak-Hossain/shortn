---
description: Explain code line by line and say what would break if each line weren't there
argument-hint: '[file path, symbol, or paste code — defaults to my current selection / last file]'
allowed-tools: Read, Bash(go doc:*)
---

# /explain-lines — Understand before you keep it

This is the project's **golden rule** in command form: _never keep code you can't explain._

Explain the target code **line by line**. The target is, in order of preference: the code in `$ARGUMENTS`, otherwise my current editor selection, otherwise the file we were last working in.

For the code in question:

1. **Walk it top to bottom.** For each meaningful line or small block, state plainly **what it does** and **what would break — concretely — if it weren't there** (e.g. "without `defer cancel()` the context leaks and the goroutine never frees its timer").
2. **Explain the Go-specific mechanics** a C++/JS dev wouldn't take for granted: pointer vs value receivers, `defer`, error wrapping with `%w`, goroutines/channels, interface satisfaction, zero values, slice aliasing, `context` propagation.
3. **Call out the design choices** — why this pattern over an alternative, and the tradeoff.
4. **Flag edge cases and failure modes** this code does NOT handle ("what breaks under load / on a nil / on a cancelled context?").
5. End with **one or two questions back to me** to check I actually understood — the kind an interviewer would ask about this exact code.

Do not rewrite or "improve" the code unless I ask. The goal is understanding, not changes.

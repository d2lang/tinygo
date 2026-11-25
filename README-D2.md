# TinyGo for D2

This is the TinyGo that [D2](https://github.com/d2lang/d2) builds its
WebAssembly module with. It is upstream TinyGo 0.44.0 plus the changes on the
`d2` branch, which exist to make that module small.

D2 used to be built with the asyncify scheduler, which TinyGo needs on
WebAssembly to switch goroutines and, since 0.44, to unwind panics. Asyncify
nearly doubles the code. D2 never switches goroutines: its host calls in, D2
computes, D2 returns. So it is now built without a scheduler and with panics
unwound by checks the compiler emits after calls:

    tinygo build -target=wasm -scheduler=none -panic-unwind=explicit \
      -buildmode=c-shared -tags=goroutines.inert -stack-size=4MB -no-debug ./d2js

What that needed here:

- `interp` kept evaluating package initializers at compile time under
  `-panic-unwind=explicit`. Upstream stops at the first package that must
  initialize at runtime, which in practice is `os`, and builds all later static
  data in code at startup.
- `syscall/js` callbacks (`js.FuncOf`) work without a scheduler.
- `-stack-size` sizes the main stack when there is no scheduler.
- With the `goroutines.inert` build tag, a `go` statement starts nothing and a
  timer never fires, instead of failing the build. Libraries start housekeeping
  goroutines (a clock for regular-expression timeouts, for one) that could not
  run during a synchronous call under a cooperative scheduler either. Waiting
  for such a goroutine deadlocks.

Since then, for size and correctness:

- The flag that tells callers a panic is unwinding is a WebAssembly global
  instead of a word in memory, which makes each of the checks smaller.
- Stack objects for the collector are no longer linked into a chain: on
  WebAssembly the collector scans the stack itself and never walked it.
- A call from JavaScript that traps (a stack overflow, say) gives its stack
  back, so the next call works; and waiting on a channel or lock with no
  goroutine to wake it is a fatal deadlock instead of a hang.
- The table of global roots stores a run of equally spaced ranges (the
  strings of a table, say) as one entry instead of one per element.
- Two kinds of pointers were not roots and could be freed while in use: a
  value read from a map, and a global that LLVM retyped to an integer because
  it is only ever copied whole. `-tags=gc.stress` builds a collector that
  makes such bugs show: it collects at every allocation and poisons what it
  frees.

Releases are tagged `v<upstream version>-d2.<n>` and built by
`.github/workflows/d2-release.yml`.

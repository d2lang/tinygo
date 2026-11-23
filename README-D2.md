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
      -buildmode=c-shared -tags=goroutines.inert -stack-size=1MB -no-debug ./d2js

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

Releases are tagged `v<upstream version>-d2.<n>` and built by
`.github/workflows/d2-release.yml`.

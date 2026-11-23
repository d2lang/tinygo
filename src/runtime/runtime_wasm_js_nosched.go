//go:build wasm && !wasi && scheduler.none && !wasip1 && !wasip2 && !wasm_unknown

package runtime

// resume is called by wasm_exec.js to deliver a js.Func call. Without a
// scheduler the handler runs to completion on the caller's stack.
//
//export resume
func resume() {
	handleEvent()
}

// go_scheduler is what wasm_exec.js calls when a sleep it was asked to time
// has elapsed. There is nothing to wake up.
//
//export go_scheduler
func go_scheduler() {
}

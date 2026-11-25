//go:build wasm && !wasi && scheduler.none && !wasip1 && !wasip2 && !wasm_unknown

package runtime

import (
	"internal/task"
	"unsafe"
)

// resumeCall is what a call of resume has to put back if a trap ends it.
//
// A trap ends every WebAssembly function that is running, but it leaves the
// stack pointer and the rest of the program's state where they were. With a
// scheduler that costs one goroutine and its stack. Without one, every call
// runs on the same stack: the next call would start where the trap happened,
// and after a stack overflow that is the end of the stack.
type resumeCall struct {
	outer        *resumeCall    // the call that this one is nested in, if any
	stackPointer uintptr        // where the stack was when the call started
	deferFrame   unsafe.Pointer // the defer frame that was current then
}

// resumeCalls is the innermost call of resume that is still running. A handler
// can call JavaScript that calls another handler, so these nest.
var resumeCalls *resumeCall

// resume is called by wasm_exec.js to deliver a js.Func call. Without a
// scheduler the handler runs to completion on the caller's stack.
//
//export resume
func resume() {
	// This function must not need a stack frame of its own, so that the stack
	// pointer it reads is the one that its caller had.
	resumeAt(getCurrentStackPointer())
}

//go:noinline
func resumeAt(stackPointer uintptr) {
	current := task.Current()
	call := &resumeCall{
		outer:        resumeCalls,
		stackPointer: stackPointer,
		deferFrame:   current.DeferFrame,
	}
	resumeCalls = call
	// With a scheduler the handler runs in a goroutine of its own, and a panic
	// that it does not recover ends the program. It must not unwind into the
	// code that called the JavaScript that called this handler instead.
	current.DeferFrame = nil
	handleEvent()
	current.DeferFrame = call.deferFrame
	resumeCalls = call.outer
}

// resume_failed is called by wasm_exec.js after a trap, or an exception from
// JavaScript, ended a call of resume. It gives the stack that the call was
// using back.
//
//export resume_failed
func resumeFailed() {
	// Like resume, this function must not need a stack frame: it would put the
	// stack pointer back to where it was on entry when it returns.
	setCurrentStackPointer(endFailedResume())
}

// endFailedResume puts the state of the innermost call of resume back, apart
// from the stack pointer, which it returns.
//
//go:noinline
func endFailedResume() uintptr {
	call := resumeCalls
	if call == nil {
		return getCurrentStackPointer()
	}
	resumeCalls = call.outer
	task.Current().DeferFrame = call.deferFrame
	abandonUnwind()
	return call.stackPointer
}

//export tinygo_setCurrentStackPointer
func setCurrentStackPointer(stackPointer uintptr)

// go_scheduler is what wasm_exec.js calls when a sleep it was asked to time
// has elapsed. There is nothing to wake up.
//
//export go_scheduler
func go_scheduler() {
}

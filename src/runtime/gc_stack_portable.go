//go:build (gc.conservative || gc.custom || gc.precise || gc.boehm) && tinygo.wasm

package runtime

import (
	"internal/task"
	"unsafe"
)

func gcMarkReachable() {
	markStack()
	markGlobals()
}

// markStack marks all root pointers found on the stack.
//
//   - Goroutine stacks are heap allocated. markTaskStacks scans the live part
//     of each, or the GC scans each as a whole object where it cannot.
//   - The system stack (aka startup stack) is not heap allocated, so even
//     though it may be referenced it will not be scanned by default.
//
// WebAssembly keeps local variables where nothing can read them, so the
// compiler makes every function store the pointers that it still needs when
// a collection can run in a stack object: memory on these stacks, which is
// scanned here like the rest of them.
func markStack() {
	// Scan the system stack.
	var sysSP uintptr
	if task.OnSystemStack() {
		// We are on the system stack.
		// Use the current stack pointer.
		sysSP = getCurrentStackPointer()
	} else {
		// We are in a goroutine.
		// Use the saved stack pointer.
		sysSP = savedStackPointer
	}
	markRoots(sysSP, stackTop)
	markTaskStacks()
}

// trackPointer is a stub function call inserted by the compiler during IR
// construction. Calls to it are later replaced with regular stack bookkeeping
// code.
func trackPointer(ptr, alloca unsafe.Pointer)

func gcResumeWorld() {
	// Nothing to do here (single threaded).
}

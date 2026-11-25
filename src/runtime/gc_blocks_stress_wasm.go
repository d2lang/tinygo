//go:build (gc.conservative || gc.precise) && gc.stress && tinygo.wasm

package runtime

import (
	"internal/task"
	"unsafe"
)

// gcStressBeforeMark clears the unused part of the system stack.
//
// The stack is scanned conservatively and frames are not cleared when they are
// set up. A pointer that a finished call left behind can end up in a part of a
// later frame that has not been written yet, and root an object by accident.
//
// The linker puts the stack at the start of the memory (--stack-first), so
// everything below the stack pointer is unused stack.
func gcStressBeforeMark() {
	if !task.OnSystemStack() {
		return
	}
	// Nothing is called below, so there is no frame under this stack pointer.
	// The word at address zero is left alone: it is never part of a frame,
	// and writing to it would look like writing through a nil pointer.
	sp := getCurrentStackPointer()
	for addr := unsafe.Sizeof(sp); addr+unsafe.Sizeof(sp) <= sp; addr += unsafe.Sizeof(sp) {
		*(*uintptr)(unsafe.Pointer(addr)) = 0
	}
}

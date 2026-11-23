//go:build scheduler.none && !goroutines.inert

package task

import "unsafe"

//go:noinline
func start(fn uintptr, args unsafe.Pointer, stackSize uintptr) {
	// The compiler will error if this is reachable.
	runtimePanicSchedulerDisabled()
}

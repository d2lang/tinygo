//go:build scheduler.none && goroutines.inert

package task

import "unsafe"

// start is what a go statement compiles to. Without a scheduler the only
// goroutine is the caller, which cannot block, so a goroutine started here
// could not run before the caller is done. With the goroutines.inert build
// tag it never runs at all, instead of failing the build. That is what a
// background goroutine experiences today during one synchronous call into a
// cooperatively scheduled program; anything that waits for the goroutine
// deadlocks, loudly.
//
// This lets a program that is driven entirely by calls from its host link
// libraries that start housekeeping goroutines, such as a clock for
// timeouts.
func start(fn uintptr, args unsafe.Pointer, stackSize uintptr) {
}

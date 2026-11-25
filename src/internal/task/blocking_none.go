//go:build scheduler.none

package task

// CanBlock reports whether a goroutine that parks itself can be resumed by
// another one. Without a scheduler there is no other one.
const CanBlock = false

// Deadlock stops the program: the only goroutine is about to wait for an
// event that nothing can deliver. It is called before the goroutine is linked
// into a wait queue, so the queue never holds a pointer to a frame that is
// gone, and it is fatal like the deadlock report of the Go runtime, so it
// cannot be swallowed by recover.
func Deadlock() {
	runtimeFatal("all goroutines are asleep - deadlock!")
}

//go:build (gc.conservative || gc.precise) && gc.stress

package runtime

// A collector for testing the compiler, selected with -tags=gc.stress.
//
// The block collector only runs when the heap is full. An object that nothing
// roots usually survives until the program is done with it, so a compiler that
// fails to root a pointer produces programs that work by luck.
//
// With this tag every allocation first runs a full collection, and freed
// blocks are overwritten with gcStressPoison. A program that still uses an
// unrooted object then traps on the first pointer it loads from it, or prints
// something else than it should. To keep such a bug from hiding:
//
//   - allocations come from the longest free range, so freed blocks keep the
//     pattern as long as possible;
//   - the collector stops the program when it finds the pattern where it looks
//     for pointers;
//   - on WebAssembly the unused part of the stack is cleared before the stack
//     is scanned, see gcStressBeforeMark.
//
// This is far too slow for anything but tests. A program that allocates too
// much to be run this way can collect at every Nth allocation instead, by
// calling the exported function tinygo_gcStressSetPeriod.

const gcStress = true

// gcStressPoison is what freed blocks are filled with. It is odd, so it is not
// an aligned pointer, and on WebAssembly it lies far above any memory a test
// will ever have, so loading through it traps.
const gcStressPoison = 0xDEADBEE1

var (
	// gcStressPeriod is the number of allocations per collection. Zero turns
	// the extra collections off.
	gcStressPeriod uint32 = 1

	// gcStressCountdown is the number of allocations left until the next
	// collection.
	gcStressCountdown uint32
)

// gcStressSetPeriod makes every nth allocation collect (the next one first),
// or none of them if n is zero.
//
//export tinygo_gcStressSetPeriod
func gcStressSetPeriod(n uint32) {
	gcStressPeriod = n
	gcStressCountdown = 0
}

// gcStressDue returns whether this allocation has to collect first.
func gcStressDue() bool {
	if gcStressPeriod == 0 {
		return false
	}
	if gcStressCountdown != 0 {
		gcStressCountdown--
		return false
	}
	gcStressCountdown = gcStressPeriod - 1
	return true
}

// gcStressPoisonBlock overwrites a block that is about to be freed.
func gcStressPoisonBlock(block gcBlock) {
	words := (*[wordsPerBlock]uintptr)(block.pointer())
	for i := range words {
		words[i] = gcStressPoison
	}
}

// gcStressPoisonFound is called when the collector comes across the poison
// pattern at addr while it looks for pointers.
func gcStressPoisonFound(addr uintptr) {
	runtimeFatal("gc stress: found a pointer that was read from freed memory")
}

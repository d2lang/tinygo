//go:build tinygo.unwind.explicit && tinygo.wasm && !scheduler.cores && !scheduler.threads

package runtime

// The compiler checks this flag after every call that can panic, which makes
// the check the most repeated piece of code in a program. The compiler keeps
// the flag in a WebAssembly global instead of linear memory (see getGlobal),
// which takes a third of the instructions to read: no address, no load.
//
// A WebAssembly global holds a whole number type, hence not a bool.
var unwindPendingSignal uint32

//go:inline
func getUnwindSignal() bool {
	return unwindPendingSignal != 0
}

//go:inline
func setUnwindSignal(unwinding bool) {
	if unwinding {
		unwindPendingSignal = 1
	} else {
		unwindPendingSignal = 0
	}
}

//go:build tinygo.unwind.explicit || tinygo.unwind.asyncify

package runtime

//go:inline
func unwindPending() bool {
	return getUnwindSignal()
}

// abandonUnwind forgets a panic that was on its way up the stack when the
// stack it was on ceased to exist.
func abandonUnwind() {
	setUnwindSignal(false)
}

//go:inline
func clearUnwind() {
	setUnwindSignal(false)
	frame := currentDeferFrame()
	if frame != nil {
		frame.PanicState &^= panicUnwinding
	}
}

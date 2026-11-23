//go:build scheduler.none && !goroutines.inert

package runtime

func addTimer(tim *timerNode) {
	runtimePanic(errTimersUnsupported)
}

func reAddTimer(tn *timerNode) {
	runtimePanic(errTimersUnsupported)
}

func removeTimer(tim *timer) *timerNode {
	runtimePanic(errTimersUnsupported)
	return nil
}

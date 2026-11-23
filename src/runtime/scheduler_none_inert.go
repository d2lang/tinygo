//go:build scheduler.none && goroutines.inert

package runtime

// With inert goroutines (see internal/task) nothing ever runs besides the
// caller, so a timer can be started and stopped but never fires. Timers are
// still queued so that stopping one reports whether it was pending, as it
// would under a scheduler that had not got round to it yet.

func addTimer(tim *timerNode) {
	timerQueueAdd(tim)
}

func reAddTimer(tn *timerNode) {
	timerQueueAdd(tn)
}

func removeTimer(tim *timer) *timerNode {
	return timerQueueRemove(tim)
}

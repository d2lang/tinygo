//go:build !scheduler.none

package task

const CanBlock = true

func Deadlock() {}

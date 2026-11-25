//go:build (gc.conservative || gc.precise) && gc.stress && !tinygo.wasm

package runtime

func gcStressBeforeMark() {}

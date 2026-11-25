//go:build (gc.conservative || gc.precise) && !gc.stress

package runtime

// gcStress is set by the gc.stress build tag, see gc_blocks_stress.go. Without
// that tag none of the code behind it is compiled in.
const gcStress = false

const gcStressPoison = 0

func gcStressDue() bool { return false }

func gcStressBeforeMark() {}

func gcStressPoisonBlock(block gcBlock) {}

func gcStressPoisonFound(addr uintptr) {}

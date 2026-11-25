package transform_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/tinygo-org/tinygo/transform"
	"tinygo.org/x/go-llvm"
)

// The pass checks itself in these tests: no integer that may be a pointer is
// left without a slot across a collection (see transform.GCSlotCheck).
var gcSlotCheck struct {
	sync.Mutex
	failures []string
}

func init() {
	transform.GCSlotCheck = func(function, value, deliveredTo string) {
		gcSlotCheck.Lock()
		defer gcSlotCheck.Unlock()
		gcSlotCheck.failures = append(gcSlotCheck.failures, function+": "+value+" has no slot, delivered to "+deliveredTo)
	}
}

func checkGCSlots(t *testing.T) {
	gcSlotCheck.Lock()
	defer gcSlotCheck.Unlock()
	for _, failure := range gcSlotCheck.failures {
		t.Error(failure)
	}
	gcSlotCheck.failures = nil
}

func TestMakeGCStackSlots(t *testing.T) {
	t.Parallel()
	testTransform(t, "testdata/gc-stackslots", func(mod llvm.Module) {
		transform.MakeGCStackSlots(mod)
	})
	checkGCSlots(t)
}

// TestGCSlotLiveness checks which pointers get a stack slot, which do not, and
// what is exposed instead.
func TestGCSlotLiveness(t *testing.T) {
	t.Parallel()
	testTransform(t, "testdata/gc-slotliveness", func(mod llvm.Module) {
		transform.MakeGCStackSlots(mod)
	})
	checkGCSlots(t)
}

func TestMakeGCGlobalRootsAVR(t *testing.T) {
	t.Parallel()
	testTransform(t, "testdata/gc-globals-avr", func(mod llvm.Module) {
		transform.MakeGCStackSlots(mod)
	})
}

// TestGCStackSlotCounts checks slot counts without comparing unrelated IR to a golden file.
// See https://github.com/tinygo-org/tinygo/pull/5762#discussion_r4113188988.
func TestGCStackSlotCounts(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		// The inputs of the acyclic merge feeding this loop do not need slots.
		"acyclicPhiIntoLoop": 3,
		// Every input here does, and must keep them.
		"nestedLoopPhis": 4,
	}

	ctx := llvm.NewContext()
	defer ctx.Dispose()
	buf, err := llvm.NewMemoryBufferFromFile("testdata/gc-slotcounts.ll")
	if err != nil {
		t.Fatalf("could not read file: %v", err)
	}
	mod, err := ctx.ParseIR(buf)
	if err != nil {
		t.Fatalf("could not load module:\n%v", err)
	}
	defer mod.Dispose()

	transform.MakeGCStackSlots(mod)
	if err := llvm.VerifyModule(mod, llvm.PrintMessageAction); err != nil {
		t.Fatal("IR verification failed")
	}

	for name, want := range want {
		fn := mod.NamedFunction(name)
		if fn.IsNil() {
			t.Errorf("%s: not found in module", name)
			continue
		}
		got, err := stackSlotCount(fn)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %d stack slots, want %d", name, got, want)
		}
	}
}

// stackSlotCount returns the number of pointer slots in the gc.stackobject of
// fn, which has a field for each of them.
func stackSlotCount(fn llvm.Value) (int, error) {
	entry := fn.EntryBasicBlock()
	for inst := entry.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
		if inst.IsAAllocaInst().IsNil() || inst.Name() != "gc.stackobject" {
			continue
		}
		return inst.AllocatedType().StructElementTypesCount(), nil
	}
	return 0, errors.New("no gc.stackobject in entry block")
}

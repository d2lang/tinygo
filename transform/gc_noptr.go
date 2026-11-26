package transform

import (
	"tinygo.org/x/go-llvm"
)

// noPointerMDName is the metadata that MarkNoPointerLoads puts on a load.
const noPointerMDName = "tinygo.noptr"

// MarkNoPointerLoads marks every load of a value without pointers (an
// integer, a float, or an aggregate of those), before LLVM optimizes the
// module.
//
// At that point the loads are those the front end made, with the type of
// the Go value they load, and the copies the interpreter made of them. An
// integer such a load reads is an integer in Go as well, so MakeGCStackSlots
// need not take it for a pointer: a uintptr keeps nothing alive in Go, and
// one that a function turns into a pointer is followed back from the
// conversion anyway. The loads that LLVM makes later, such as the integer
// loads it copies small objects with, are not marked.
//
// LLVM keeps metadata it does not know on a load it duplicates, which still
// reads the same Go value, and drops it from a load it changes or merges with
// another. So a mark can get lost, which costs a slot, but it does not move
// to a load of a pointer.
func MarkNoPointerLoads(mod llvm.Module) {
	ctx := mod.Context()
	kind := ctx.MDKindID(noPointerMDName)
	node := ctx.MDNode(nil)
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
				if !inst.IsALoadInst().IsNil() && isNoPointerType(inst.Type()) {
					inst.SetMetadata(kind, node)
				}
			}
		}
	}
}

// isNoPointerType returns whether a value of this type is made of integers
// and floats only.
func isNoPointerType(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.IntegerTypeKind, llvm.FloatTypeKind, llvm.DoubleTypeKind:
		return true
	case llvm.StructTypeKind:
		for _, field := range typ.StructElementTypes() {
			if !isNoPointerType(field) {
				return false
			}
		}
		return true
	case llvm.ArrayTypeKind:
		return typ.ArrayLength() == 0 || isNoPointerType(typ.ElementType())
	}
	return false
}

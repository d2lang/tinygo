package transform

import (
	"fmt"

	"tinygo.org/x/go-llvm"
)

// GCSlotCheck, if set, is called for every value that MakeGCStackSlots leaves
// without a slot although a pointer in it crosses a collection: a check of
// the pass against itself, for tests and for measuring on real programs. See
// checkSlots.
var GCSlotCheck func(function, value, usedBy string)

// checkSlots walks back from every use of every value that may hold
// a pointer (a pointer, an aggregate with one, an integer that may be one) to
// where its bits come from, and reports a value that has no slot although it
// should: one that a load or a call made, a conversion to a pointer or a phi
// in a loop, with a collecting call between it and the use. A pointer that is
// passed to a collecting call needs its slot during the call.
//
// liveSlots decides the other way round, forward from what is in use at
// a collecting call. The check shares only the classification of the values
// with it (hasPointer, intBits), not the search.
func (f *gcSlotFunc) checkSlots(live []llvm.Value) {
	n := int32(len(f.insts))
	if n == 0 {
		return
	}
	function := f.insts[0].InstructionParent().Parent().Name()
	kept := make([]bool, n)
	keptField := map[int32]map[string]struct{}{}
	for _, value := range live {
		if idx, ok := f.index[value]; ok {
			kept[idx] = true
		}
		var path []uint32
		base := value
		for !base.IsAExtractValueInst().IsNil() {
			path = append(base.Indices(), path...)
			base = base.Operand(0)
		}
		if idx, ok := f.index[base]; ok && len(path) != 0 {
			if keptField[idx] == nil {
				keptField[idx] = map[string]struct{}{}
			}
			keptField[idx][fmt.Sprint(path)] = struct{}{}
		}
	}

	// holds returns whether a slot holds the part of value that is needed (the
	// field at path, or all of it), from the definition of value on.
	holds := func(value int32, path []uint32) bool {
		if kept[value] {
			return true
		}
		typ := f.insts[value].Type()
		if typ.TypeKind() != llvm.StructTypeKind && typ.TypeKind() != llvm.ArrayTypeKind {
			return false
		}
		fields := pointerPaths(typ, nil, nil)
		if f.intBits[value] {
			fields = f.carrierPaths(typ, nil, fields)
		}
		for _, field := range fields {
			if !pathsOverlap(field, path) {
				continue
			}
			if _, ok := keptField[value][fmt.Sprint(field)]; !ok {
				return false
			}
		}
		return true
	}

	type state struct {
		value, at       int32
		crossed, chosen bool
		path            string
	}
	seen := map[state]struct{}{}
	var sink int32
	// walk looks at value, whose bits are needed just before instruction at
	// of block. crossed says that a collecting call lies between there and the
	// use the walk started from. chosen says that, for those calls, the value
	// goes unchanged into a value that a slot holds and that liveSlots passes
	// through (a conversion to a pointer, a phi in a loop), so that slot holds
	// it; an address computed on the way ends that. path is the field that is
	// needed if value is an aggregate.
	var walk func(value, block, at int32, path []uint32, crossed, chosen bool)
	walk = func(value, block, at int32, path []uint32, crossed, chosen bool) {
		if value < 0 || !f.hasPointer[value] && !f.intBits[value] {
			return
		}
		key := state{value: value, at: at, crossed: crossed, chosen: chosen}
		if len(path) != 0 {
			key.path = fmt.Sprint(path)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if f.collectionBetween(value, block, at) {
			// The value itself is in use across a collection.
			crossed, chosen = true, false
		}
		held := holds(value, path)
		if crossed && !chosen && f.isSlotKind(value) && !held {
			switch typ := f.insts[value].Type(); typ.TypeKind() {
			case llvm.PointerTypeKind, llvm.StructTypeKind, llvm.ArrayTypeKind:
				GCSlotCheck(function, f.insts[value].String(), f.insts[sink].String())
			default:
				// A value too narrow for a pointer has nothing a slot could
				// hold.
				if f.wholeSlot(typ) {
					GCSlotCheck(function, f.insts[value].String(), f.insts[sink].String())
				}
			}
		}
		if f.kind[value] != slotFlow {
			// A load or a call made the bits.
			return
		}
		switch {
		case held && f.passesThrough(value):
			// Its slot holds what goes into it unchanged.
			chosen = true
		case held:
			// The slot holds the value from here on, and so what it is made
			// of. Only the way here is left.
			crossed, chosen = false, false
		case chosen && !f.keepsValue(value):
			// An address is computed on the way to the slot: what it is
			// computed from has to be held.
			chosen = false
		}
		from := f.opStart[value]
		switch f.opcode[value] {
		case llvm.PHI:
			for slot := from; slot < f.opStart[value+1]; slot++ {
				pred := f.opBlock[slot]
				walk(f.ops[slot], pred, f.blockStart[pred+1], path, crossed, chosen)
			}
		case llvm.ExtractValue:
			walk(f.ops[from], f.blockOf[value], value, append(f.insts[value].Indices(), path...), crossed, chosen)
		case llvm.InsertValue:
			inserted := f.insts[value].Indices()
			if !pathsOverlap(inserted, path) {
				walk(f.ops[from], f.blockOf[value], value, path, crossed, chosen)
				break
			}
			if len(path) < len(inserted) {
				walk(f.ops[from], f.blockOf[value], value, path, crossed, chosen)
				path = inserted
			}
			walk(f.ops[from+1], f.blockOf[value], value, path[len(inserted):], crossed, chosen)
		default:
			for slot := from; slot < f.opStart[value+1]; slot++ {
				if f.bitsFlow(slot) {
					walk(f.ops[slot], f.blockOf[value], value, path, crossed, chosen)
				}
			}
		}
	}
	for sink = 0; sink < n; sink++ {
		for slot := f.opStart[sink]; slot < f.opStart[sink+1]; slot++ {
			value := f.ops[slot]
			if value < 0 || f.isCallee(slot) || !f.hasPointer[value] && !f.intBits[value] {
				continue
			}
			if f.kind[sink] == slotFlow && f.bitsFlow(slot) {
				// The bits go on into the result, so they are needed where
				// that is used, and the walk from there comes back here.
				continue
			}
			block, at := f.blockOf[sink], sink
			if f.opcode[sink] == llvm.PHI {
				// A phi takes the value at the end of the block it comes from.
				block = f.opBlock[slot]
				at = f.blockStart[block+1]
			}
			typ := f.insts[value].Type()
			switch {
			case !f.collecting[sink] || !f.hasPointer[value]:
				walk(value, block, at, nil, false, false)
			case typ.TypeKind() == llvm.PointerTypeKind:
				// A pointer passed to a collecting call needs a slot during
				// the call: the callee does not root its parameters.
				walk(value, block, at, nil, true, false)
			default:
				// So do the pointers in an aggregate. Its integers do not:
				// the callee takes them as integers.
				walk(value, block, at, nil, false, false)
				for _, path := range pointerPaths(typ, nil, nil) {
					walk(value, block, at, path, true, false)
				}
			}
		}
	}
}

// collectionBetween returns whether a collecting call can run after the
// instruction def and before the instruction at of block, where def is used.
func (f *gcSlotFunc) collectionBetween(def, block, at int32) bool {
	f.stampID++
	f.stack = f.stack[:0]
	for {
		if f.blockOf[def] == block && def < at {
			if f.collectBefore[at] != f.collectBefore[def+1] {
				return true
			}
		} else {
			if f.collectBefore[at] != f.collectBefore[f.blockStart[block]] {
				return true
			}
			for _, pred := range f.preds[block] {
				if f.stamp[pred] != f.stampID {
					f.stamp[pred] = f.stampID
					f.stack = append(f.stack, pred)
				}
			}
		}
		if len(f.stack) == 0 {
			return false
		}
		block = f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		at = f.blockStart[block+1]
	}
}

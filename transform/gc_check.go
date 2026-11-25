package transform

import (
	"fmt"

	"tinygo.org/x/go-llvm"
)

// GCSlotCheck, if set, is called for every integer that MakeGCStackSlots
// leaves without a slot although it may be a pointer that crosses a
// collection: a check of the pass against itself, for tests and for measuring
// on real programs. See checkCarriers.
var GCSlotCheck func(function, value, deliveredTo string)

// checkCarriers walks back from every place where an integer is delivered (an
// inttoptr or an address computed from integers, the value of a store, an
// argument of a call, a return) to where its bits come from, and reports what
// came out of memory or a call, crosses a collecting call on that way, and is
// not among the values that got a slot. liveSlots works the other way round:
// forward from what is live at a call.
func (f *gcSlotFunc) checkCarriers(live []llvm.Value) {
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

	type state struct {
		value, at    int32
		crossed, all bool
		path         string
	}
	seen := map[state]struct{}{}
	var sink int32
	report := func(value int32) {
		GCSlotCheck(function, f.insts[value].String(), f.insts[sink].String())
	}
	// fromOrigin returns whether value can have bits that came out of memory
	// or a call.
	reached := map[[2]int32]bool{}
	fromOrigin := func(value int32, all bool) bool {
		key := [2]int32{value, 0}
		if all {
			key[1] = 1
		}
		if result, ok := reached[key]; ok {
			return result
		}
		visited := map[int32]struct{}{value: {}}
		stack := []int32{value}
		result := false
		for len(stack) != 0 && !result {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			typ := f.insts[v].Type()
			if typ.TypeKind() == llvm.PointerTypeKind || !all && !f.canCarry(typ) {
				continue
			}
			if f.kind[v] == slotOrigin {
				result = f.canCarry(typ)
				continue
			}
			if f.kind[v] != slotFlow {
				continue
			}
			from, to := f.carrierOperands(v)
			if all {
				from, to = f.opStart[v], f.opStart[v+1]
			}
			for slot := from; slot < to; slot++ {
				operand := f.ops[slot]
				if _, ok := visited[operand]; !ok && operand >= 0 && !f.isCallee(slot) {
					visited[operand] = struct{}{}
					stack = append(stack, operand)
				}
			}
		}
		reached[key] = result
		return result
	}
	// walk looks at value, whose bits are needed just before instruction at
	// of block. crossed says that a collecting call lies between there and
	// the place they are delivered, all that they are on their way to become
	// an address, so that arithmetic counts. path is the field that is
	// needed if value is an aggregate.
	var walk func(value, block, at int32, path []uint32, crossed, all bool)
	walk = func(value, block, at int32, path []uint32, crossed, all bool) {
		if value < 0 || kept[value] {
			return
		}
		typ := f.insts[value].Type()
		if typ.TypeKind() == llvm.PointerTypeKind {
			return
		}
		key := state{value: value, at: at, crossed: crossed, all: all}
		if len(path) != 0 {
			key.path = fmt.Sprint(path)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		crossed = crossed || f.collectionBetween(value, block, at)
		// The slot of what goes into a phi in a loop is overwritten on the
		// way round, so such a phi is the place if it has such bits at all.
		cyclic := f.opcode[value] == llvm.PHI && f.inCycle[f.blockOf[value]]
		if crossed && (f.kind[value] == slotOrigin || cyclic && fromOrigin(value, all)) {
			// This is where the bits have to be in a slot.
			if typ.TypeKind() == llvm.IntegerTypeKind {
				if f.canCarry(typ) {
					report(value)
				}
				return
			}
			for _, field := range f.carrierPaths(typ, nil, nil) {
				if !pathsOverlap(field, path) {
					continue
				}
				if _, ok := keptField[value][fmt.Sprint(field)]; !ok {
					report(value)
				}
			}
			return
		}
		if f.kind[value] != slotFlow {
			return
		}
		from, to := f.carrierOperands(value)
		if all {
			from, to = f.opStart[value], f.opStart[value+1]
		} else if !f.canCarry(typ) {
			return
		}
		switch f.opcode[value] {
		case llvm.PHI:
			for slot := from; slot < to; slot++ {
				pred := f.opBlock[slot]
				walk(f.ops[slot], pred, f.blockStart[pred+1], path, crossed, all)
			}
		case llvm.ExtractValue:
			path = append(f.insts[value].Indices(), path...)
			walk(f.ops[from], f.blockOf[value], value, path, crossed, all)
		case llvm.InsertValue:
			inserted := f.insts[value].Indices()
			if !pathsOverlap(inserted, path) {
				walk(f.ops[from], f.blockOf[value], value, path, crossed, all)
				break
			}
			if len(path) < len(inserted) {
				walk(f.ops[from], f.blockOf[value], value, path, crossed, all)
				path = inserted
			}
			walk(f.ops[from+1], f.blockOf[value], value, path[len(inserted):], crossed, all)
		default:
			for slot := from; slot < to; slot++ {
				if !f.isCallee(slot) {
					walk(f.ops[slot], f.blockOf[value], value, path, crossed, all)
				}
			}
		}
	}
	for sink = 0; sink < n; sink++ {
		all := f.opcode[sink] == llvm.IntToPtr || f.isIntegerAddress(sink)
		switch {
		case all, f.opcode[sink] == llvm.Store, f.opcode[sink] == llvm.Ret, f.kind[sink] == slotOrigin:
		default:
			continue
		}
		for slot := f.opStart[sink]; slot < f.opStart[sink+1]; slot++ {
			value := f.ops[slot]
			if value < 0 || f.isCallee(slot) {
				continue
			}
			if all || f.canCarry(f.insts[value].Type()) {
				walk(value, f.blockOf[sink], sink, nil, false, all)
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

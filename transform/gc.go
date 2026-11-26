package transform

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/tinygo-org/tinygo/compiler/llvmutil"
	"tinygo.org/x/go-llvm"
)

// This is somewhat ugly to access through the API.
// https://github.com/llvm/llvm-project/blob/94ebcfd16dac67486bae624f74e1c5c789448bae/llvm/include/llvm/Support/ModRef.h#L62
// https://github.com/llvm/llvm-project/blob/94ebcfd16dac67486bae624f74e1c5c789448bae/llvm/include/llvm/Support/ModRef.h#L87
const shiftExcludeArgMem = 2

// MakeGCStackSlots converts all calls to runtime.trackPointer to explicit
// stores to stack slots that are scannable by the GC.
//
// A pointer only gets a slot if a collection can find it in use: see
// gcSlotFunc for the rule, and for what keeps it true in the code that LLVM
// makes of the result.
func MakeGCStackSlots(mod llvm.Module) bool {
	hasGlobalRoots := makeGCGlobalRoots(mod)

	// Check whether there are allocations at all.
	alloc := mod.NamedFunction("runtime.alloc")
	if alloc.IsNil() {
		// Nothing to do. Without allocations there is nothing to track.
		for _, call := range getUses(mod.NamedFunction("runtime.trackPointer")) {
			call.EraseFromParentAsInstruction()
		}
		return hasGlobalRoots
	}

	trackPointer := mod.NamedFunction("runtime.trackPointer")
	if trackPointer.IsNil() || trackPointer.FirstUse().IsNil() {
		return hasGlobalRoots
	}

	ctx := mod.Context()
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	targetData := llvm.NewTargetData(mod.DataLayout())
	defer targetData.Dispose()

	// All functions that call runtime.alloc needs stack objects.
	trackFuncs := map[llvm.Value]struct{}{}
	markParentFunctions(trackFuncs, alloc)

	// The functions without a body that may run a collection, and the
	// functions of the runtime that do.
	collectors := map[llvm.Value]struct{}{alloc: {}}
	for _, name := range []string{"runtime.GC", "runtime.runGC"} {
		if fn := mod.NamedFunction(name); !fn.IsNil() {
			collectors[fn] = struct{}{}
			markParentFunctions(trackFuncs, fn)
		}
	}

	// External functions may indirectly suspend the goroutine or perform a heap allocation.
	// Their callers should get stack objects.
	memAttr := llvm.AttributeKindID("memory")
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if _, ok := trackFuncs[fn]; ok {
			continue // already found
		}
		if !fn.FirstBasicBlock().IsNil() {
			// This is not an external function.
			continue
		}
		if fn == trackPointer {
			// Manually exclude trackPointer.
			continue
		}

		mem := fn.GetEnumFunctionAttribute(memAttr)
		if !mem.IsNil() && mem.GetEnumValue()>>shiftExcludeArgMem == 0 {
			// This does not access non-argument memory.
			// Exclude it.
			continue
		}

		// The callers need stack objects.
		collectors[fn] = struct{}{}
		markParentFunctions(trackFuncs, fn)
	}

	// Look at all other functions to see whether they contain function pointer
	// calls.
	// This takes less than 5ms for ~100kB of WebAssembly but would perhaps be
	// faster when written in C++ (to avoid the CGo overhead).
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if _, ok := trackFuncs[fn]; ok {
			continue // already found
		}

	scanBody:
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for call := bb.FirstInstruction(); !call.IsNil(); call = llvm.NextInstruction(call) {
				if call.IsACallInst().IsNil() {
					continue // only looking at calls
				}
				called := call.CalledValue()
				if !called.IsAFunction().IsNil() {
					continue // only looking for function pointers
				}
				trackFuncs[fn] = struct{}{}
				markParentFunctions(trackFuncs, fn)
				break scanBody
			}
		}
	}

	// The garbage collector finds the stack objects by reading the stack,
	// which LLVM knows nothing about: it would drop every store to a stack
	// object, as nothing that it can see reads one. So each function hands its
	// stack object to a function that LLVM cannot look into and that, for all
	// LLVM knows, keeps the address for whatever is called next. In truth it
	// does nothing, see asm_tinygowasm.S.
	ptrType := llvm.PointerType(ctx.Int8Type(), 0)
	exposeType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{ptrType}, false)
	expose := mod.NamedFunction(exposeStackObjectName)
	if expose.IsNil() {
		expose = llvm.AddFunction(mod, exposeStackObjectName, exposeType)
	}

	pass := &gcSlotPass{
		ctx:          ctx,
		builder:      builder,
		targetData:   targetData,
		trackPointer: trackPointer,
		expose:       expose,
		exposeType:   exposeType,
		trackFuncs:   trackFuncs,
		collectors:   collectors,
		// Without the table of global roots the collector reads globals in
		// a way that LLVM cannot see, so nothing below can rely on a pointer
		// in a global being there. Give every pointer a slot, as before.
		prune:         hasGlobalRoots,
		noAliasKind:   llvm.AttributeKindID("noalias"),
		readOnlyKind:  llvm.AttributeKindID("readonly"),
		readNoneKind:  llvm.AttributeKindID("readnone"),
		noCaptureKind: llvm.AttributeKindID(llvmutil.NoCaptureAttrName()),
		twiceKind:     llvm.AttributeKindID("returns_twice"),
		noPointerKind: ctx.MDKindID(noPointerMDName),
	}
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.FirstBasicBlock().IsNil() {
			continue
		}
		if _, ok := trackFuncs[fn]; !ok {
			// This function nor any of the functions it calls (recursively)
			// allocate anything from the heap, so it will not trigger a garbage
			// collection cycle. Thus, it does not need to track local pointer
			// values.
			continue
		}
		pass.run(fn)
	}

	// What is left are the calls in functions that cannot collect.
	for _, call := range getUses(trackPointer) {
		call.EraseFromParentAsInstruction()
	}

	return true
}

// gcSlotPass is the state MakeGCStackSlots shares between functions.
type gcSlotPass struct {
	ctx          llvm.Context
	builder      llvm.Builder
	targetData   llvm.TargetData
	trackPointer llvm.Value
	expose       llvm.Value
	exposeType   llvm.Type
	trackFuncs   map[llvm.Value]struct{}
	collectors   map[llvm.Value]struct{}
	prune        bool

	noAliasKind, readOnlyKind, readNoneKind, noCaptureKind, twiceKind uint

	// noPointerKind is the metadata kind of MarkNoPointerLoads.
	noPointerKind int
}

// run gives fn its stack object.
func (p *gcSlotPass) run(fn llvm.Value) {
	// Find all calls to runtime.trackPointer in this function.
	var calls []llvm.Value
	for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
			if inst.InstructionOpcode() == llvm.Call && inst.CalledValue() == p.trackPointer {
				calls = append(calls, inst)
			}
		}
	}

	// Determine what to do with each call.
	var pointers []llvm.Value
	rooted := make(map[llvm.Value]struct{}, len(calls))
	var cyclicPHIs []llvm.Value
	for _, call := range calls {
		ptr := call.Operand(0)
		call.EraseFromParentAsInstruction()

		// Some trivial optimizations.
		if ptr.IsAInstruction().IsNil() {
			continue
		}
		if _, ok := rooted[ptr]; ok {
			continue
		}
		switch ptr.InstructionOpcode() {
		case llvm.GetElementPtr:
			// Check for all zero offsets.
			// Sometimes LLVM rewrites bitcasts to zero-index GEPs, and we still need to track the GEP.
			n := ptr.OperandsCount()
			var hasOffset bool
			for i := 1; i < n; i++ {
				offset := ptr.Operand(i)
				if offset.IsAConstantInt().IsNil() || offset.ZExtValue() != 0 {
					hasOffset = true
					break
				}
			}

			if hasOffset {
				// These values do not create new values: the values already
				// existed locally in this function so must have been tracked
				// already.
				continue
			}
		case llvm.PHI:
			// A phi gets a slot of its own because its value can change,
			// so tracking an input instead is not enough. A phi that can
			// be re-evaluated has its slot overwritten on the way round
			// and so only roots the value the current iteration selected,
			// so remember it and give its inputs slots below.
			if blockInCycle(ptr.InstructionParent()) {
				cyclicPHIs = append(cyclicPHIs, ptr)
			}
		case llvm.ExtractValue, llvm.BitCast:
			// These instructions do not create new values, but their
			// original value may not be tracked. So keep tracking them for
			// now.
			// With more analysis, it should be possible to optimize a
			// significant chunk of these away.
		case llvm.Call, llvm.Load, llvm.IntToPtr:
			// These create new values so must be stored locally. But
			// perhaps some of these can be fused when they actually refer
			// to the same value.
		default:
			// Ambiguous. These instructions are uncommon, but perhaps could
			// be optimized if needed.
		}

		if ptr := stripPointerCasts(ptr); !ptr.IsAAllocaInst().IsNil() {
			// Allocas don't need to be tracked because they are allocated
			// on the C stack which is scanned separately.
			continue
		}
		rooted[ptr] = struct{}{}
		pointers = append(pointers, ptr)
	}

	// Give every input of a tracked phi that can repeat a slot of its own.
	// SimplifyCFG may have merged the trackPointer calls of two unrelated
	// pointers into a single call on this phi, leaving whichever pointer
	// the phi did not select unrooted while it is still live.
	//
	// Inputs are followed only through phis that can themselves repeat.
	// A merge that runs at most once stores its slot once and never
	// overwrites it, so that slot continues to root the selected value
	// for the rest of the frame.
	//
	// Inputs are not screened by the non-zero-offset GEP rule the loop
	// above applies, so an interior pointer whose base is already tracked
	// can pick up a slot of its own. That is another slot rather than
	// another root, so it is left alone for now.
	if len(cyclicPHIs) != 0 {
		expanded := make(map[llvm.Value]struct{}, len(cyclicPHIs))
		worklist := append([]llvm.Value(nil), cyclicPHIs...)
		for len(worklist) != 0 {
			phi := worklist[len(worklist)-1]
			worklist = worklist[:len(worklist)-1]
			if _, ok := expanded[phi]; ok {
				// Phis can be cyclic, so only expand each one once.
				continue
			}
			expanded[phi] = struct{}{}
			for i := 0; i < phi.IncomingCount(); i++ {
				incoming := phi.IncomingValue(i)
				if incoming.IsAInstruction().IsNil() {
					// Constants and arguments cannot be given a slot here.
					continue
				}
				if incoming.InstructionOpcode() == llvm.PHI && blockInCycle(incoming.InstructionParent()) {
					worklist = append(worklist, incoming)
				}
				if stripped := stripPointerCasts(incoming); !stripped.IsAAllocaInst().IsNil() {
					// Allocas live on the C stack, which is scanned separately.
					continue
				}
				if _, ok := rooted[incoming]; ok {
					continue
				}
				rooted[incoming] = struct{}{}
				pointers = append(pointers, incoming)
			}
		}
	}

	exposed := false
	if p.prune {
		// Only keep the pointers that a collection can find in use, and
		// keep LLVM from carrying the others across a collection.
		f := p.newSlotFunc(fn)
		pointers = f.liveSlots(pointers)
		exposed = f.expose(pointers)
	}

	if len(pointers) != 0 {
		// The stack object has a slot for every pointer.
		fields := make([]llvm.Type, len(pointers))
		for i, ptr := range pointers {
			fields[i] = ptr.Type()
		}
		stackObjectType := p.ctx.StructType(fields, false)

		// Create the stack object at the function entry, with nothing in it.
		p.builder.SetInsertPointBefore(fn.EntryBasicBlock().FirstInstruction())
		stackObject := p.builder.CreateAlloca(stackObjectType, "gc.stackobject")
		p.builder.CreateStore(llvm.ConstNull(stackObjectType), stackObject)
		p.builder.CreateCall(p.exposeType, p.expose, []llvm.Value{stackObject}, "")
		exposed = true

		// Do a store to the stack object after each new pointer that is created.
		for i, ptr := range pointers {
			// Insert the store after the pointer value is created.
			p.builder.SetInsertPointBefore(instructionAfter(ptr))

			// Extract a pointer to the appropriate section of the stack object.
			gep := p.builder.CreateGEP(stackObjectType, stackObject, []llvm.Value{
				llvm.ConstInt(p.ctx.Int32Type(), 0, false),
				llvm.ConstInt(p.ctx.Int32Type(), uint64(i), false),
			}, "")

			// Store the pointer into the stack slot.
			p.builder.CreateStore(ptr, gep)
		}
	}

	if exposed {
		// What this function exposed has to stay as it is across every call
		// that the function makes: its stack object keeps the arguments of
		// those calls alive, and the collector reads all of it. LLVM takes a
		// call marked 'tail' to leave the caller's stack alone, and would
		// move a store into a stack object or a variable past such a call.
		// Calls after the first exposed pointer do not get the mark again.
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
				if inst.InstructionOpcode() == llvm.Call && inst.IsTailCall() {
					inst.SetTailCall(false)
				}
			}
		}
	}
}

// instructionAfter returns the first place after the definition of value
// where an instruction can be inserted.
func instructionAfter(value llvm.Value) llvm.Value {
	next := llvm.NextInstruction(value)
	for !next.IsAPHINode().IsNil() {
		// PHI nodes are required to be at the start of the block.
		next = llvm.NextInstruction(next)
	}
	return next
}

// Opcodes that go-llvm has no name for. See LLVMOpcode in llvm-c/Core.h.
const (
	opBr            llvm.Opcode = 2
	opFence         llvm.Opcode = 55
	opAtomicCmpXchg llvm.Opcode = 56
	opAtomicRMW     llvm.Opcode = 57
	opAddrSpaceCast llvm.Opcode = 60
	opFNeg          llvm.Opcode = 66
	opFreeze        llvm.Opcode = 68
	opPtrToAddr     llvm.Opcode = 69
)

// What an instruction does with the values it is given.
const (
	slotUses   uint8 = iota // only uses them
	slotFlow                // computes its result from them: a cast, a phi, ...
	slotOrigin              // gets its result from memory or from a call
)

// gcSlotFunc decides which pointers of a function need a stack slot.
//
// The collector can only run inside a call that reaches it, a collecting
// call. A pointer needs a slot if it is still in use when such a call is made
// or when it returns: as an argument (a callee does not root its parameters,
// the caller does), or after the call, anywhere the call can lead to.
// Otherwise no collection can tell the difference.
//
// "In use" covers every value the pointer can be found in: the values computed
// from it without going through memory or a call (interior pointers, casts,
// phis, selects, fields of aggregates, the integer after a conversion and
// whatever is computed from that, a comparison included). The search for what
// a value in use is made of goes back to where its bits entered the function,
// and that value gets the slot. It goes on past a conversion to a pointer and
// past a phi in a loop, which get slots of their own, see passesThrough.
//
// The pointers that are looked at are not just the tracked ones but every
// value that a load, a call, an integer conversion or a phi in a loop
// produces: all the places where a pointer into the heap can enter the
// function. Each is rooted where it is made.
//
// A pointer does not have to look like one. LLVM copies a small object (one or
// two pointers, a string, an interface) as an integer of its size, and once
// the variable it was copied to is gone that integer is the only thing that
// has the pointers. So the integers of at least the size of a pointer that
// loads produce are taken for possible pointers (see findCarriers). They get
// a slot, as the integers they are (the collector reads the stack word by
// word), on the same terms as pointers: if they, or anything computed from
// them, are in use across a collecting call, whatever the use is. A comparison
// after a collection, for one, would find a new object at the address of the
// old one. Being passed to the collecting call itself does not count for an
// integer, though: a function takes the integers it is given for integers.
//
// The pass runs before link time optimization, and it is the final code that
// has to obey the rule, not the code seen here. Stores and calls keep their
// order, so a pointer with a slot is in its slot before the call. What must
// not happen is that LLVM carries a pointer without a slot across a collecting
// call: by moving a load or a store past the call, or by turning memory that
// holds the pointer during the call into a value. LLVM can do that only with
// memory that, as far as it knows, no call reads or writes. So no such memory
// may be on the heap, or hold a pointer into the heap while a collection runs:
//
//   - A new allocation that gets no slot is passed to exposeStackObjectName,
//     like a stack object.
//   - So is a stack variable that may hold such a pointer when a collection
//     runs, as a pointer or as such an integer. It then stays in memory,
//     where the collector finds it.
//   - Globals with pointers have their address in the table of global roots.
//   - No call stays marked 'tail' in a function that exposes part of its
//     stack: LLVM takes such a call to leave the caller's stack alone.
type gcSlotFunc struct {
	pass *gcSlotPass

	// The instructions in order. An instruction is known by its index.
	insts      []llvm.Value
	index      map[llvm.Value]int32
	opcode     []llvm.Opcode
	kind       []uint8
	blockOf    []int32
	blockStart []int32 // index of the first instruction, with one extra entry
	preds      [][]int32
	succs      [][]int32
	inCycle    []bool // per block

	// The operands that are instructions (-1 for the others) and, for a
	// phi, the blocks they come from.
	opStart  []int32
	ops      []int32
	opBlock  []int32
	opInst   []int32 // the instruction an operand belongs to
	useStart []int32
	uses     []int32 // indices into ops

	// collectBefore[i] is the number of collecting calls before instruction i.
	collecting    []bool
	collectBefore []int32
	calls         []int32 // the collecting calls

	// hasPointer says that the type of the value contains a pointer. intBits
	// says that a pointer may sit in it as an integer, or in an integer field
	// of it (see findCarriers). fromHere says that the pointer was not handed
	// to the function by its caller: a load or a call made it, or it is
	// computed from the result of one.
	hasPointer []bool
	intBits    []bool
	fromHere   []bool

	// boundary marks the values that may get a slot of their own. The search
	// for what a pointer in use is made of stops at them, except where
	// passesThrough says otherwise.
	boundary []bool

	// conservative is set for a function with something this analysis does
	// not understand. It keeps every tracked pointer.
	conservative bool

	// variables are the stack variables that have to be exposed.
	variables []llvm.Value

	stamp   []int32
	stampID int32
	stack   []int32
}

// gcSlot is a pointer that may get a slot.
type gcSlot struct {
	value llvm.Value // nil if it still has to be extracted from the aggregate
	def   int32      // index of value, or -1
	agg   int32      // index of the aggregate that value is a field of, or -1
	path  []uint32   // which field
	ints  bool       // the field is an integer
}

func (p *gcSlotPass) newSlotFunc(fn llvm.Value) *gcSlotFunc {
	f := &gcSlotFunc{pass: p, index: map[llvm.Value]int32{}}

	blockIndex := map[llvm.BasicBlock]int32{}
	for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		blockIndex[bb] = int32(len(f.blockStart))
		f.blockStart = append(f.blockStart, int32(len(f.insts)))
		for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
			f.index[inst] = int32(len(f.insts))
			f.insts = append(f.insts, inst)
			f.blockOf = append(f.blockOf, blockIndex[bb])
		}
	}
	f.blockStart = append(f.blockStart, int32(len(f.insts)))
	numBlocks := len(f.blockStart) - 1
	f.preds = make([][]int32, numBlocks)
	succs := make([][]int32, numBlocks)
	for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		term := bb.LastInstruction()
		from := blockIndex[bb]
		for i := 0; i < term.SuccessorsCount(); i++ {
			to := blockIndex[term.Successor(i)]
			succs[from] = append(succs[from], to)
			f.preds[to] = append(f.preds[to], from)
		}
	}
	f.succs = succs
	f.inCycle = blocksInCycles(succs)
	f.stamp = make([]int32, numBlocks)

	n := len(f.insts)
	f.opcode = make([]llvm.Opcode, n)
	f.kind = make([]uint8, n)
	f.collecting = make([]bool, n)
	f.collectBefore = make([]int32, n+1)
	f.hasPointer = make([]bool, n)
	f.intBits = make([]bool, n)
	f.fromHere = make([]bool, n)
	f.boundary = make([]bool, n)
	f.opStart = make([]int32, n+1)
	for i, inst := range f.insts {
		op := inst.InstructionOpcode()
		f.opcode[i] = op
		f.opStart[i] = int32(len(f.ops))
		numOperands := inst.OperandsCount()
		for j := 0; j < numOperands; j++ {
			operand := int32(-1)
			if idx, ok := f.index[inst.Operand(j)]; ok {
				operand = idx
			}
			f.ops = append(f.ops, operand)
			f.opInst = append(f.opInst, int32(i))
			block := int32(-1)
			if op == llvm.PHI {
				block = blockIndex[inst.IncomingBlock(j)]
			}
			f.opBlock = append(f.opBlock, block)
		}

		typ := inst.Type()
		switch typ.TypeKind() {
		case llvm.VoidTypeKind, llvm.IntegerTypeKind, llvm.FloatTypeKind, llvm.DoubleTypeKind:
		case llvm.PointerTypeKind, llvm.StructTypeKind, llvm.ArrayTypeKind:
			f.hasPointer[i] = typeHasPointers(typ)
		default:
			// A vector, perhaps of pointers.
			f.conservative = true
		}

		switch op {
		case llvm.GetElementPtr, llvm.BitCast, opAddrSpaceCast, llvm.PtrToInt, opPtrToAddr,
			llvm.IntToPtr, llvm.Trunc, llvm.ZExt, llvm.SExt, llvm.PHI, llvm.Select,
			llvm.ExtractValue, llvm.InsertValue, llvm.ExtractElement, llvm.InsertElement,
			llvm.ShuffleVector, opFreeze, llvm.Add, llvm.Sub, llvm.Mul, llvm.UDiv, llvm.SDiv,
			llvm.URem, llvm.SRem, llvm.Shl, llvm.LShr, llvm.AShr, llvm.And, llvm.Or, llvm.Xor:
			f.kind[i] = slotFlow
		case llvm.Load, opAtomicRMW, opAtomicCmpXchg, llvm.VAArg:
			f.kind[i] = slotOrigin
		case llvm.Call:
			f.kind[i] = slotOrigin
			callee := inst.CalledValue().IsAFunction()
			switch {
			case callee.IsNil():
				// A function pointer or inline assembly.
				f.collecting[i] = true
			case callee.IntrinsicID() != 0:
				// Intrinsics do not call back into the program.
				f.kind[i] = slotFlow
			default:
				_, external := p.collectors[callee]
				_, internal := p.trackFuncs[callee]
				f.collecting[i] = external || internal
				if !callee.GetEnumFunctionAttribute(p.twiceKind).IsNil() {
					f.conservative = true
				}
			}
			if !inst.GetCallSiteEnumAttribute(-1, p.twiceKind).IsNil() {
				// Control can come back to this call from any later one.
				f.conservative = true
			}
		case llvm.ICmp, llvm.FCmp, llvm.FAdd, llvm.FSub, llvm.FMul, llvm.FDiv, llvm.FRem, opFNeg,
			llvm.FPToUI, llvm.FPToSI, llvm.UIToFP, llvm.SIToFP, llvm.FPTrunc, llvm.FPExt,
			llvm.Alloca, llvm.Store, opFence, llvm.Ret, opBr, llvm.Switch, llvm.IndirectBr,
			llvm.Unreachable:
			f.kind[i] = slotUses
		default:
			// An invoke, exception handling, something new.
			f.conservative = true
		}
		f.collectBefore[i+1] = f.collectBefore[i]
		if f.collecting[i] {
			f.collectBefore[i+1]++
			f.calls = append(f.calls, int32(i))
		}
	}
	f.opStart[n] = int32(len(f.ops))

	// The uses of every instruction.
	f.useStart = make([]int32, n+1)
	for _, operand := range f.ops {
		if operand >= 0 {
			f.useStart[operand+1]++
		}
	}
	for i := 0; i < n; i++ {
		f.useStart[i+1] += f.useStart[i]
	}
	f.uses = make([]int32, f.useStart[n])
	fill := append([]int32(nil), f.useStart[:n]...)
	for slot, operand := range f.ops {
		if operand >= 0 {
			f.uses[fill[operand]] = int32(slot)
			fill[operand]++
		}
	}

	f.findCarriers()
	return f
}

// canCarry returns whether a value of this type has room for a pointer in an
// integer.
func (f *gcSlotFunc) canCarry(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.IntegerTypeKind:
		return typ.IntTypeWidth() >= f.pass.targetData.PointerSize()*8
	case llvm.StructTypeKind:
		for _, field := range typ.StructElementTypes() {
			if f.canCarry(field) {
				return true
			}
		}
	case llvm.ArrayTypeKind:
		return typ.ArrayLength() != 0 && f.canCarry(typ.ElementType())
	case llvm.VectorTypeKind:
		// LLVM vectorizes nothing before this pass, but a vector of
		// pointers or of such integers is kept like an integer.
		elem := typ.ElementType()
		return elem.TypeKind() == llvm.PointerTypeKind || f.canCarry(elem)
	}
	return false
}

// wholeSlot returns whether a value of this type, which is neither a pointer
// nor an aggregate, is kept in a slot as it is if it may have a pointer in its
// bits: whether it has room for one.
func (f *gcSlotFunc) wholeSlot(typ llvm.Type) bool {
	switch typ.TypeKind() {
	case llvm.IntegerTypeKind, llvm.FloatTypeKind, llvm.DoubleTypeKind, llvm.VectorTypeKind:
		return f.pass.targetData.TypeStoreSize(typ) >= uint64(f.pass.targetData.PointerSize())
	}
	return false
}

// findCarriers finds the values that may have a pointer in their integer
// bits (intBits), and the values computed from something that a load or
// a call in this function made (fromHere).
//
// A pointer gets into an integer by a conversion, or as one of the integers
// LLVM copies small objects with (one or two pointers, a string, an
// interface). So every integer of at least the size of a pointer that a load
// or an atomic operation produces may be one, also as a field of an
// aggregate, unless the load is marked as one of a value of a Go type without
// pointers (see MarkNoPointerLoads). What a call returns is not: the result
// types of functions are those of the Go source, which LLVM does not change
// at the levels TinyGo optimizes at (argument promotion runs only at O3).
// Whatever an address is computed from is a pointer for sure, whatever its
// type and wherever it comes from. And whatever is computed from a value that
// may have a pointer in it, through any calculation, may have its bits too.
func (f *gcSlotFunc) findCarriers() {
	n := int32(len(f.insts))
	sure := make([]bool, n)
	f.stack = f.stack[:0]
	mark := func(value int32) {
		if value >= 0 && !sure[value] && f.insts[value].Type().TypeKind() != llvm.PointerTypeKind {
			sure[value] = true
			f.stack = append(f.stack, value)
		}
	}
	for i := int32(0); i < n; i++ {
		if f.opcode[i] == llvm.IntToPtr || f.isIntegerAddress(i) {
			for slot := f.opStart[i]; slot < f.opStart[i+1]; slot++ {
				mark(f.ops[slot])
			}
		}
	}
	for len(f.stack) != 0 {
		value := f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		if f.kind[value] != slotFlow {
			continue
		}
		for slot := f.opStart[value]; slot < f.opStart[value+1]; slot++ {
			if !f.isCallee(slot) {
				mark(f.ops[slot])
			}
		}
	}

	for i := int32(0); i < n; i++ {
		switch {
		case sure[i], f.opcode[i] == llvm.PtrToInt, f.opcode[i] == opPtrToAddr:
			f.intBits[i] = true
		case f.kind[i] == slotOrigin && f.opcode[i] != llvm.Call:
			f.intBits[i] = f.canCarry(f.insts[i].Type()) && !f.noPointers(i)
		}
	}
	for changed := true; changed; {
		changed = false
		for i := int32(0); i < n; i++ {
			if !f.fromHere[i] {
				here := f.kind[i] == slotOrigin
				if f.kind[i] == slotFlow {
					for slot := f.opStart[i]; slot < f.opStart[i+1]; slot++ {
						if operand := f.ops[slot]; operand >= 0 && f.fromHere[operand] {
							here = true
						}
					}
				}
				if here {
					f.fromHere[i] = true
					changed = true
				}
			}
			if f.intBits[i] || f.kind[i] != slotFlow || f.insts[i].Type().TypeKind() == llvm.PointerTypeKind {
				// A pointer again (inttoptr, getelementptr) is followed as one.
				continue
			}
			for slot := f.opStart[i]; slot < f.opStart[i+1]; slot++ {
				if operand := f.ops[slot]; operand >= 0 && f.intBits[operand] && f.bitsFlow(slot) {
					f.intBits[i] = true
					changed = true
					break
				}
			}
		}
	}
}

// bitsFlow returns whether the bits of the operand in this slot can be found
// in the result of the instruction it belongs to. All operands of a flow
// instruction count, except the callee, the condition of a select (it only
// chooses), and the distance of a shift.
func (f *gcSlotFunc) bitsFlow(slot int32) bool {
	user := f.opInst[slot]
	switch f.opcode[user] {
	case llvm.Select:
		return slot != f.opStart[user]
	case llvm.Shl, llvm.LShr, llvm.AShr:
		return slot == f.opStart[user]
	}
	return !f.isCallee(slot)
}

// noPointers returns whether the instruction is a load that the front end
// marked as one of a value of a Go type without pointers.
func (f *gcSlotFunc) noPointers(i int32) bool {
	return f.opcode[i] == llvm.Load && !f.insts[i].Metadata(f.pass.noPointerKind).IsNil()
}

// isIntegerAddress returns whether the instruction computes an address from
// integers alone, the other way to write an inttoptr.
func (f *gcSlotFunc) isIntegerAddress(i int32) bool {
	return f.opcode[i] == llvm.GetElementPtr && f.insts[i].Operand(0).IsNull()
}

// isSlotKind returns whether the instruction is one that may get a slot: one
// that brings a value into the function (a load, a call, an atomic
// operation), a conversion to a pointer, or a phi in a loop, whose value can
// outlive the slot of the value that went in, which is overwritten on the way
// round.
func (f *gcSlotFunc) isSlotKind(i int32) bool {
	return f.kind[i] == slotOrigin || f.opcode[i] == llvm.IntToPtr || f.isCyclicPhi(i)
}

// isCyclicPhi returns whether the instruction is a phi in a loop.
func (f *gcSlotFunc) isCyclicPhi(i int32) bool {
	return f.opcode[i] == llvm.PHI && f.inCycle[f.blockOf[i]]
}

// passesThrough returns whether the search for what a pointer in use is made
// of goes on past user, which has a slot: past a conversion to a pointer and
// past a phi in a loop. Their value may point past the end of the object it
// was computed from (a pointer that runs one element ahead, an address
// computed in integers), and then only the slot of what it was computed from
// keeps the object. So the search goes on as for a value that is chosen into
// the slot (see keepsValue), and what an address is computed from on the way
// is needed itself.
func (f *gcSlotFunc) passesThrough(user int32) bool {
	return f.opcode[user] == llvm.IntToPtr || f.isCyclicPhi(user)
}

// keepsValue returns whether the instruction passes a value on without
// computing an address from it: a cast, a choice (a phi, a select), or
// putting the value into an aggregate or taking it out. A getelementptr only
// counts if all its offsets are zero. What a slot holds that way is the value
// itself.
func (f *gcSlotFunc) keepsValue(i int32) bool {
	switch f.opcode[i] {
	case llvm.PHI, llvm.Select, llvm.BitCast, opAddrSpaceCast, opFreeze, llvm.ExtractValue,
		llvm.InsertValue, llvm.Trunc, llvm.ZExt, llvm.SExt, llvm.PtrToInt, opPtrToAddr, llvm.IntToPtr:
		return true
	case llvm.GetElementPtr:
		inst := f.insts[i]
		for j := 1; j < inst.OperandsCount(); j++ {
			offset := inst.Operand(j)
			if offset.IsAConstantInt().IsNil() || offset.ZExtValue() != 0 {
				return false
			}
		}
		return true
	}
	return false
}

// blocksInCycles returns which blocks lie on a cycle, see blockInCycle.
func blocksInCycles(succs [][]int32) []bool {
	// Tarjan's algorithm for strongly connected components, without recursion.
	n := len(succs)
	inCycle := make([]bool, n)
	order := make([]int32, n) // 0 means not visited yet
	low := make([]int32, n)
	onStack := make([]bool, n)
	var stack []int32
	type frame struct{ block, next int32 }
	var frames []frame
	counter := int32(0)
	for start := range succs {
		if order[start] != 0 {
			continue
		}
		frames = append(frames, frame{int32(start), 0})
		for len(frames) != 0 {
			fr := &frames[len(frames)-1]
			b := fr.block
			if fr.next == 0 {
				counter++
				order[b] = counter
				low[b] = counter
				stack = append(stack, b)
				onStack[b] = true
			}
			if int(fr.next) < len(succs[b]) {
				s := succs[b][fr.next]
				fr.next++
				if s == b {
					inCycle[b] = true
				}
				if order[s] == 0 {
					frames = append(frames, frame{s, 0})
				} else if onStack[s] && order[s] < low[b] {
					low[b] = order[s]
				}
				continue
			}
			frames = frames[:len(frames)-1]
			if len(frames) != 0 {
				parent := frames[len(frames)-1].block
				if low[b] < low[parent] {
					low[parent] = low[b]
				}
			}
			if low[b] == order[b] {
				// b is the root of a component.
				size := 0
				for i := len(stack) - 1; ; i-- {
					size++
					if stack[i] == b {
						break
					}
				}
				for i := 0; i < size; i++ {
					member := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[member] = false
					if size > 1 {
						inCycle[member] = true
					}
				}
			}
		}
	}
	return inCycle
}

// carries returns whether the pointer in operand, which user takes in this
// slot, can end up in user.
func (f *gcSlotFunc) carries(operand, user, slot int32) bool {
	if f.kind[user] != slotFlow || !f.bitsFlow(slot) {
		return false
	}
	if !f.hasPointer[operand] && !f.intBits[operand] {
		return false
	}
	// A value without pointers only carries one as an integer.
	return f.hasPointer[user] || f.intBits[user]
}

// isCallee returns whether the operand in this slot is the function that is
// called. That is never a pointer into the heap.
func (f *gcSlotFunc) isCallee(slot int32) bool {
	user := f.opInst[slot]
	return f.opcode[user] == llvm.Call && slot == f.opStart[user+1]-1
}

// liveAtCollection returns whether value is in use when a collecting call is
// made or when it returns, looking only at the uses that skip does not reject.
// With args set, being an argument of the collecting call counts too: the
// callee does not root its parameters, the caller does. That goes for
// pointers, not for integers: an integer that a function is given is an
// integer to it.
func (f *gcSlotFunc) liveAtCollection(value int32, skip func(user int32) bool, args bool) bool {
	f.stampID++
	f.stack = f.stack[:0]
	defBlock := f.blockOf[value]
	// liveOut says that value is needed at the end of block. It returns
	// whether a collecting call lies between the definition and that point,
	// and otherwise remembers to look at the blocks before it.
	liveOut := func(block int32) bool {
		if f.stamp[block] == f.stampID {
			return false
		}
		f.stamp[block] = f.stampID
		end := f.blockStart[block+1]
		if block == defBlock {
			return f.collectBefore[end] != f.collectBefore[value+1]
		}
		if f.collectBefore[end] != f.collectBefore[f.blockStart[block]] {
			return true
		}
		f.stack = append(f.stack, block)
		return false
	}
	for u := f.useStart[value]; u < f.useStart[value+1]; u++ {
		slot := f.uses[u]
		user := f.opInst[slot]
		if skip != nil && skip(user) {
			continue
		}
		if f.opcode[user] == llvm.ExtractValue && !f.hasPointer[user] && !f.intBits[user] {
			// This takes a field without a pointer out of an aggregate.
			continue
		}
		if f.opcode[user] == llvm.PHI {
			// The phi uses the value at the end of the block it comes from.
			if liveOut(f.opBlock[slot]) {
				return true
			}
			continue
		}
		if args && f.collecting[user] && !f.isCallee(slot) {
			return true
		}
		block := f.blockOf[user]
		if block == defBlock && user > value {
			if f.collectBefore[user] != f.collectBefore[value+1] {
				return true
			}
			continue
		}
		if f.collectBefore[user] != f.collectBefore[f.blockStart[block]] {
			return true
		}
		f.stack = append(f.stack, block)
	}
	// The value is needed at the start of the blocks on the stack, so at the
	// end of all blocks before them.
	for len(f.stack) != 0 {
		block := f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		for _, pred := range f.preds[block] {
			if liveOut(pred) {
				return true
			}
		}
	}
	return false
}

// source follows a pointer back through the instructions that only pass it
// on: casts, and putting it into an aggregate and taking it out again. It
// returns the instruction that made the pointer (def), or the aggregate that
// the pointer is a field of and which field (agg and path). Both are -1 if
// nothing in this function made the pointer: it is a parameter or a constant,
// or a field of one.
func (f *gcSlotFunc) source(ptr llvm.Value) (def, agg int32, path []uint32) {
	for {
		idx, ok := f.index[ptr]
		if !ok {
			return -1, -1, nil
		}
		switch f.opcode[idx] {
		case llvm.BitCast:
			ptr = ptr.Operand(0)
		case llvm.GetElementPtr:
			for i := 1; i < ptr.OperandsCount(); i++ {
				offset := ptr.Operand(i)
				if offset.IsAConstantInt().IsNil() || offset.ZExtValue() != 0 {
					return idx, -1, nil
				}
			}
			ptr = ptr.Operand(0)
		case llvm.ExtractValue:
			path = ptr.Indices()
			from := ptr.Operand(0)
			for {
				agg, ok := f.index[from]
				if !ok {
					return -1, -1, nil
				}
				if f.opcode[agg] == llvm.ExtractValue {
					path = append(from.Indices(), path...)
					from = from.Operand(0)
					continue
				}
				if f.opcode[agg] != llvm.InsertValue {
					return -1, agg, path
				}
				inserted := from.Indices()
				if len(inserted) > len(path) || !slices.Equal(inserted, path[:len(inserted)]) {
					// Another field was inserted.
					from = from.Operand(0)
					continue
				}
				// This field, or the aggregate it is in, was inserted here.
				from = from.Operand(1)
				path = path[len(inserted):]
				if len(path) == 0 {
					break
				}
			}
			ptr = from
		default:
			return idx, -1, nil
		}
	}
}

// carrierPaths appends the index path of every integer in a value of type typ
// that has room for a pointer.
func (f *gcSlotFunc) carrierPaths(typ llvm.Type, prefix []uint32, paths [][]uint32) [][]uint32 {
	switch typ.TypeKind() {
	case llvm.IntegerTypeKind:
		if f.canCarry(typ) {
			paths = append(paths, append([]uint32(nil), prefix...))
		}
	case llvm.StructTypeKind:
		for i, field := range typ.StructElementTypes() {
			paths = f.carrierPaths(field, append(prefix, uint32(i)), paths)
		}
	case llvm.ArrayTypeKind:
		if f.canCarry(typ.ElementType()) {
			for i := 0; i < typ.ArrayLength(); i++ {
				paths = f.carrierPaths(typ.ElementType(), append(prefix, uint32(i)), paths)
			}
		}
	}
	return paths
}

// pathsOverlap returns whether one field contains the other.
func pathsOverlap(a, b []uint32) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pointerPaths appends the index path of every pointer in a value of type typ.
func pointerPaths(typ llvm.Type, prefix []uint32, paths [][]uint32) [][]uint32 {
	switch typ.TypeKind() {
	case llvm.PointerTypeKind:
		paths = append(paths, append([]uint32(nil), prefix...))
	case llvm.StructTypeKind:
		for i, field := range typ.StructElementTypes() {
			paths = pointerPaths(field, append(prefix, uint32(i)), paths)
		}
	case llvm.ArrayTypeKind:
		if typeHasPointers(typ.ElementType()) {
			for i := 0; i < typ.ArrayLength(); i++ {
				paths = pointerPaths(typ.ElementType(), append(prefix, uint32(i)), paths)
			}
		}
	}
	return paths
}

// liveSlots returns the pointers that need a slot. It looks at the tracked
// pointers and at every other value that brings a pointer into the function,
// and takes each of them where it is made: several tracked pointers are often
// the same field of the result of one call.
func (f *gcSlotFunc) liveSlots(tracked []llvm.Value) []llvm.Value {
	f.findVariables()
	if f.conservative {
		// Every tracked pointer, and every other value that may bring
		// a pointer into the function, in use at a collecting call or not.
		have := make(map[llvm.Value]struct{}, len(tracked))
		for _, ptr := range tracked {
			have[ptr] = struct{}{}
		}
		add := func(value llvm.Value) {
			if _, ok := have[value]; !ok {
				have[value] = struct{}{}
				tracked = append(tracked, value)
			}
		}
		for i, inst := range f.insts {
			if !f.isSlotKind(int32(i)) || !f.hasPointer[i] && !f.intBits[i] {
				continue
			}
			typ := inst.Type()
			switch typ.TypeKind() {
			case llvm.PointerTypeKind:
				add(inst)
			case llvm.StructTypeKind, llvm.ArrayTypeKind:
				for _, path := range pointerPaths(typ, nil, nil) {
					add(f.extractField(int32(i), path))
				}
				if f.intBits[i] {
					for _, path := range f.carrierPaths(typ, nil, nil) {
						add(f.extractField(int32(i), path))
					}
				}
			default:
				if f.wholeSlot(typ) {
					add(inst)
				}
			}
		}
		return tracked
	}

	var slots []gcSlot
	isSlot := make([]bool, len(f.insts))
	isField := map[string]struct{}{}
	addSlot := func(def, agg int32, path []uint32, ints bool) {
		switch {
		case def >= 0:
			if isSlot[def] {
				return
			}
			switch f.opcode[def] {
			case llvm.GetElementPtr, llvm.Alloca:
				// An interior pointer is in use as long as the pointer it is
				// computed from, and the stack is scanned as it is.
				return
			}
			isSlot[def] = true
			f.boundary[def] = true
			slots = append(slots, gcSlot{value: f.insts[def], def: def, agg: -1})
		case agg >= 0:
			key := fmt.Sprint(agg, path)
			if _, ok := isField[key]; ok {
				return
			}
			isField[key] = struct{}{}
			slots = append(slots, gcSlot{def: -1, agg: agg, path: path, ints: ints})
		}
	}
	for _, ptr := range tracked {
		def, agg, path := f.source(ptr)
		addSlot(def, agg, path, false)
	}
	for i, inst := range f.insts {
		ints := f.intBits[i]
		if !f.hasPointer[i] && !ints || !f.isSlotKind(int32(i)) {
			continue
		}
		typ := inst.Type()
		switch typ.TypeKind() {
		case llvm.PointerTypeKind:
			addSlot(int32(i), -1, nil, false)
			continue
		case llvm.StructTypeKind, llvm.ArrayTypeKind:
		default:
			// An integer, or another value that an address is computed
			// from. The slot takes it as it is: the collector reads the
			// stack word by word.
			if f.wholeSlot(typ) {
				addSlot(int32(i), -1, nil, true)
			}
			continue
		}
		for _, path := range pointerPaths(typ, nil, nil) {
			addSlot(-1, int32(i), path, false)
		}
		if ints {
			for _, path := range f.carrierPaths(typ, nil, nil) {
				addSlot(-1, int32(i), path, true)
			}
		}
		if f.opcode[i] == llvm.PHI {
			f.boundary[i] = true
		}
	}

	// needed[v] says that v, or something computed from v up to the next
	// value with a slot of its own, is in use at a collecting call.
	//
	// chosen[v] says that v goes unchanged (see keepsValue) into a value with
	// a slot that the search passes through (see passesThrough). That slot
	// holds v, so v needs no slot for that, but what an address is computed
	// from on the way there is needed.
	n := int32(len(f.insts))
	needed := make([]bool, n)
	chosen := make([]bool, n)
	var worklist []int32 // a value, or -1-value for one that is chosen
	for i := int32(0); i < n; i++ {
		if (f.hasPointer[i] || f.intBits[i]) && f.liveAtCollection(i, nil, f.hasPointer[i]) {
			needed[i] = true
			worklist = append(worklist, i)
		}
	}
	push := func(value int32, asChosen bool) {
		switch {
		case needed[value]:
		case !asChosen:
			needed[value] = true
			worklist = append(worklist, value)
		case !chosen[value]:
			chosen[value] = true
			worklist = append(worklist, -1-value)
		}
	}
	for len(worklist) != 0 {
		user := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		isChosen := user < 0
		if isChosen {
			user = -1 - user
			if needed[user] {
				// Followed as needed instead.
				continue
			}
		}
		var asChosen bool
		switch {
		case isChosen:
			asChosen = f.keepsValue(user)
		case f.boundary[user]:
			if !f.passesThrough(user) {
				// The slot of user roots what is used through it.
				continue
			}
			asChosen = true
		}
		for slot := f.opStart[user]; slot < f.opStart[user+1]; slot++ {
			operand := f.ops[slot]
			if operand < 0 || !f.carries(operand, user, slot) {
				continue
			}
			if asChosen && f.kind[operand] == slotOrigin {
				// The slot user's value goes into holds it.
				continue
			}
			push(operand, asChosen)
		}
	}

	// The same for one field of an aggregate: the other fields that are
	// taken out of it do not count.
	paths := map[int32][]uint32{}
	otherField := func(agg int32, path []uint32) func(user int32) bool {
		return func(user int32) bool {
			if f.opcode[user] != llvm.ExtractValue || f.ops[f.opStart[user]] != agg {
				return false
			}
			userPath, ok := paths[user]
			if !ok {
				userPath = f.insts[user].Indices()
				paths[user] = userPath
			}
			return !pathsOverlap(userPath, path)
		}
	}
	fieldNeeded := func(agg int32, path []uint32, ints bool) bool {
		if ints && !f.intBits[agg] {
			return false
		}
		skip := otherField(agg, path)
		for u := f.useStart[agg]; u < f.useStart[agg+1]; u++ {
			slot := f.uses[u]
			user := f.opInst[slot]
			if skip(user) || f.boundary[user] {
				// Its slot roots the field. A value with a slot that the
				// search passes through holds it as well, see chosen.
				continue
			}
			if needed[user] && f.carries(agg, user, slot) {
				return true
			}
		}
		// An integer field that is passed to a collecting call is an integer
		// to the callee.
		return f.liveAtCollection(agg, skip, !ints)
	}

	var live []llvm.Value
	for _, slot := range slots {
		switch {
		case slot.def >= 0:
			if needed[slot.def] {
				live = append(live, slot.value)
			}
		case fieldNeeded(slot.agg, slot.path, slot.ints):
			live = append(live, f.extractField(slot.agg, slot.path))
		}
	}
	if GCSlotCheck != nil {
		f.checkSlots(live)
	}
	return live
}

// extractField returns the pointer that is the given field of an aggregate,
// available before anything can collect.
func (f *gcSlotFunc) extractField(agg int32, path []uint32) llvm.Value {
	// It is often there already.
	for i := agg + 1; i < f.blockStart[f.blockOf[agg]+1] && !f.collecting[i]; i++ {
		if f.opcode[i] == llvm.ExtractValue && f.ops[f.opStart[i]] == agg && slices.Equal(f.insts[i].Indices(), path) {
			return f.insts[i]
		}
	}
	f.pass.builder.SetInsertPointBefore(instructionAfter(f.insts[agg]))
	value := f.insts[agg]
	for _, index := range path {
		value = f.pass.builder.CreateExtractValue(value, int(index), "")
	}
	return value
}

// expose passes to exposeStackObjectName what LLVM has to take for memory
// that every call may read and write: the new allocations that have no slot,
// and the stack variables that may hold a pointer. It returns whether that
// includes a stack variable.
func (f *gcSlotFunc) expose(slots []llvm.Value) bool {
	p := f.pass
	hasSlot := make(map[llvm.Value]struct{}, len(slots))
	for _, ptr := range slots {
		hasSlot[ptr] = struct{}{}
	}
	exposedAlloca := false
	for i, inst := range f.insts {
		switch f.opcode[i] {
		case llvm.Call:
			// LLVM knows that the result of a function with a noalias result
			// is a new object that nothing else knows.
			if f.kind[i] != slotOrigin || inst.Type().TypeKind() != llvm.PointerTypeKind {
				continue
			}
			if _, ok := hasSlot[inst]; ok || f.useStart[i] == f.useStart[i+1] {
				continue
			}
			noalias := !inst.GetCallSiteEnumAttribute(0, p.noAliasKind).IsNil()
			if callee := inst.CalledValue().IsAFunction(); !callee.IsNil() {
				noalias = noalias || !callee.GetEnumAttributeAtIndex(0, p.noAliasKind).IsNil()
			}
			if noalias {
				p.builder.SetInsertPointBefore(llvm.NextInstruction(inst))
				p.builder.CreateCall(p.exposeType, p.expose, []llvm.Value{inst}, "")
			}
		}
	}
	for _, variable := range f.variables {
		p.builder.SetInsertPointBefore(llvm.NextInstruction(variable))
		p.builder.CreateCall(p.exposeType, p.expose, []llvm.Value{variable}, "")
		exposedAlloca = true
	}
	return exposedAlloca
}

// findVariables collects the stack variables that have to be exposed.
func (f *gcSlotFunc) findVariables() {
	for i, inst := range f.insts {
		if f.opcode[i] == llvm.Alloca && f.holdsPointerAtCollection(inst) {
			f.variables = append(f.variables, inst)
		}
	}
}

// holdsPointerAtCollection returns whether a stack variable may hold a pointer
// into the heap while a collection runs, with the variable read afterwards:
// whether a collecting call can come after something that may write such a
// pointer to the variable and before or at a use of the variable.
func (f *gcSlotFunc) holdsPointerAtCollection(alloca llvm.Value) bool {
	var writes, uses []int32
	if !f.stackUses(alloca, &writes, &uses) {
		// Something this pass cannot follow has the address.
		return true
	}
	if len(writes) == 0 {
		return false
	}

	// The blocks that come after a write, and those from which a use can
	// still be reached.
	numBlocks := len(f.preds)
	const (
		afterWrite = 1 << iota
		beforeUse
	)
	state := make([]uint8, numBlocks)
	firstWrite := map[int32]int32{}
	lastUse := map[int32]int32{}
	f.stack = f.stack[:0]
	for _, w := range writes {
		block := f.blockOf[w]
		if first, ok := firstWrite[block]; !ok || w < first {
			firstWrite[block] = w
		}
		f.stack = append(f.stack, block)
	}
	for len(f.stack) != 0 {
		block := f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		for _, succ := range f.succs[block] {
			if state[succ]&afterWrite == 0 {
				state[succ] |= afterWrite
				f.stack = append(f.stack, succ)
			}
		}
	}
	for _, u := range uses {
		block := f.blockOf[u]
		if last, ok := lastUse[block]; !ok || u > last {
			lastUse[block] = u
		}
		f.stack = append(f.stack, block)
	}
	for len(f.stack) != 0 {
		block := f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		for _, pred := range f.preds[block] {
			if state[pred]&beforeUse == 0 {
				state[pred] |= beforeUse
				f.stack = append(f.stack, pred)
			}
		}
	}
	isWrite := map[int32]struct{}{}
	for _, w := range writes {
		isWrite[w] = struct{}{}
	}
	isUse := map[int32]struct{}{}
	for _, u := range uses {
		isUse[u] = struct{}{}
	}
	for _, call := range f.calls {
		if _, ok := isWrite[call]; ok {
			// The callee may write to the variable and then collect.
			return true
		}
		block := f.blockOf[call]
		written := state[block]&afterWrite != 0
		if first, ok := firstWrite[block]; ok && first < call {
			written = true
		}
		if !written {
			continue
		}
		_, used := isUse[call]
		if last, ok := lastUse[block]; ok && last > call {
			used = true
		}
		if used || state[block]&beforeUse != 0 {
			return true
		}
	}
	return false
}

// stackUses collects the instructions that use the memory at addr, the
// address of a stack variable or of a part of one, and those among them that
// may write a pointer into the heap there. It returns false if the address
// gets somewhere this pass cannot follow.
func (f *gcSlotFunc) stackUses(addr llvm.Value, writes, uses *[]int32) bool {
	p := f.pass
	for use := addr.FirstUse(); !use.IsNil(); use = use.NextUse() {
		user := use.User()
		idx, ok := f.index[user]
		if !ok {
			return false
		}
		switch f.opcode[idx] {
		case llvm.ICmp:
		case llvm.Load:
			*uses = append(*uses, idx)
		case llvm.Store:
			if user.Operand(0) == addr {
				// The address itself is stored somewhere.
				return false
			}
			*uses = append(*uses, idx)
			if f.mayBePointer(user.Operand(0)) {
				*writes = append(*writes, idx)
			}
		case llvm.GetElementPtr, llvm.BitCast:
			if user.Operand(0) != addr || !f.stackUses(user, writes, uses) {
				return false
			}
		case llvm.Call:
			callee := user.CalledValue()
			if callee == addr {
				return false
			}
			numArgs := user.OperandsCount() - 1
			if fn := callee.IsAFunction(); !fn.IsNil() && fn.IntrinsicID() != 0 {
				switch {
				case strings.HasPrefix(fn.Name(), "llvm.lifetime."):
				case !user.IsAMemSetInst().IsNil():
					*uses = append(*uses, idx)
				case !user.IsAMemCpyInst().IsNil(), !user.IsAMemMoveInst().IsNil():
					*uses = append(*uses, idx)
					if user.Operand(0) != addr {
						// Only copied from.
						break
					}
					source := stripPointerCasts(user.Operand(1))
					if source.IsAGlobalVariable().IsNil() || !source.IsGlobalConstant() {
						*writes = append(*writes, idx)
					}
				default:
					return false
				}
				continue
			}
			*uses = append(*uses, idx)
			for i := 0; i < numArgs; i++ {
				if user.Operand(i) != addr {
					continue
				}
				// A function may write to the variable or keep its address,
				// unless it is known to do neither.
				attrs := func(kind uint) (attr llvm.Attribute) {
					attr = user.GetCallSiteEnumAttribute(i+1, kind)
					if fn := callee.IsAFunction(); attr.IsNil() && !fn.IsNil() && i < fn.ParamsCount() {
						attr = fn.GetEnumAttributeAtIndex(i+1, kind)
					}
					return
				}
				if !llvmutil.IsNoCapture(attrs(p.noCaptureKind)) {
					return false
				}
				if attrs(p.readOnlyKind).IsNil() && attrs(p.readNoneKind).IsNil() {
					*writes = append(*writes, idx)
				}
			}
		default:
			return false
		}
	}
	return true
}

// mayBePointer returns whether value, which is stored to a stack variable,
// may be or contain a pointer into the heap.
func (f *gcSlotFunc) mayBePointer(value llvm.Value) bool {
	if !value.IsAConstant().IsNil() {
		// Null, or the address of a global.
		return false
	}
	idx, ok := f.index[value]
	if !ok || !f.fromHere[idx] {
		// A parameter, or computed from parameters: the caller roots those.
		return false
	}
	typ := value.Type()
	if typeHasPointers(typ) {
		return true
	}
	if f.pass.targetData.TypeStoreSize(typ) < uint64(f.pass.targetData.PointerSize()) {
		return false
	}
	// LLVM copies small objects as integers.
	switch f.opcode[idx] {
	case llvm.Load:
		return !f.noPointers(idx) || f.intBits[idx]
	case opAtomicRMW, opAtomicCmpXchg, llvm.VAArg:
		return true
	}
	return f.conservative || f.intBits[idx]
}

// exposeStackObjectName is the function that every function with a stack
// object passes that object to. See MakeGCStackSlots.
const exposeStackObjectName = "tinygo_exposeStackObject"

func makeGCGlobalRoots(mod llvm.Module) bool {
	rootCount := mod.NamedFunction("runtime.gcGlobalRootCount")
	rootAt := mod.NamedFunction("runtime.gcGlobalRoot")
	rootSize := mod.NamedFunction("runtime.gcGlobalRootSize")
	rootValues := mod.NamedFunction("runtime.gcGlobalRootValues")
	if rootCount.IsNil() || rootAt.IsNil() || rootSize.IsNil() ||
		!rootCount.FirstBasicBlock().IsNil() ||
		!rootAt.FirstBasicBlock().IsNil() ||
		!rootSize.FirstBasicBlock().IsNil() {
		return false
	}
	if !rootValues.IsNil() && !rootValues.FirstBasicBlock().IsNil() {
		return false
	}

	// A runtime that declares these functions also scans runs: count ranges
	// of the same size, each stride bytes after the one before it. A runtime
	// that does not gets every range in the plain table.
	runFuncs := []llvm.Value{
		mod.NamedFunction("runtime.gcGlobalRootRunCount"),
		mod.NamedFunction("runtime.gcGlobalRootRun"),
		mod.NamedFunction("runtime.gcGlobalRootRunSize"),
		mod.NamedFunction("runtime.gcGlobalRootRunStride"),
		mod.NamedFunction("runtime.gcGlobalRootRunLength"),
	}
	useRuns := true
	for _, fn := range runFuncs {
		if fn.IsNil() {
			useRuns = false
		} else if !fn.FirstBasicBlock().IsNil() {
			return false
		}
	}

	ctx := mod.Context()
	uintptrType := rootCount.GlobalValueType().ReturnType()
	ptrType := rootAt.GlobalValueType().ReturnType()
	targetData := llvm.NewTargetData(mod.DataLayout())
	defer targetData.Dispose()
	pointerSize := uint64(targetData.PointerSize())
	pointerBits := 8 * pointerSize
	rootType := ctx.StructType([]llvm.Type{ptrType, uintptrType}, false)
	runType := ctx.StructType([]llvm.Type{ptrType, uintptrType, uintptrType, uintptrType}, false)
	encoding := gcGlobalRootEncoding{
		pointerSize: pointerSize,
		rootBytes:   targetData.TypeAllocSize(rootType),
		runBytes:    targetData.TypeAllocSize(runType),
	}
	var roots []gcGlobalRootRange
	var runs []gcGlobalRootRun
	var rootWords uint64
	for global := mod.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if strings.HasPrefix(global.Name(), "llvm.") ||
			global.IsGlobalConstant() ||
			global.Initializer().IsNil() ||
			global.Type().PointerAddressSpace() != 0 { // not in memory, such as a WebAssembly global
			continue
		}
		typ := global.GlobalValueType()
		var ranges []gcGlobalRootRange
		if isRetypedPointerGlobal(typ, pointerBits) {
			// Scan the whole integer, one pointer-sized word at a time.
			size := targetData.TypeAllocSize(typ)
			ranges = appendGCGlobalRootRange(ranges, global, 0, size)
		} else if typeHasPointers(typ) {
			ranges = appendGCGlobalRootRanges(ranges, global, typ, targetData)
		}
		for _, r := range ranges {
			rootWords += r.size / pointerSize
		}
		if !useRuns {
			roots = append(roots, ranges...)
			continue
		}
		globalRoots, globalRuns := encoding.makeRuns(ranges)
		if err := encoding.check(ranges, globalRoots, globalRuns); err != nil {
			panic("transform: the root table for " + global.Name() + " does not scan the same words as its ranges: " + err.Error())
		}
		roots = append(roots, globalRoots...)
		runs = append(runs, globalRuns...)
	}

	builder := ctx.NewBuilder()
	defer builder.Dispose()

	rootInitializers := make([]llvm.Value, len(roots))
	for i, root := range roots {
		rootInitializers[i] = llvm.ConstNamedStruct(rootType, []llvm.Value{
			root.address(ctx.Int8Type(), uintptrType),
			llvm.ConstInt(uintptrType, root.size, false),
		})
	}
	rootArrayType, rootArray := addGCGlobalRootTable(mod, "runtime.gcGlobalRoots", rootType, rootInitializers)
	defineGCGlobalRootCount(builder, rootCount, len(roots))
	defineGCGlobalRootField(builder, rootAt, rootArrayType, rootArray, rootType, 0)
	defineGCGlobalRootField(builder, rootSize, rootArrayType, rootArray, rootType, 1)

	if useRuns {
		runInitializers := make([]llvm.Value, len(runs))
		for i, run := range runs {
			runInitializers[i] = llvm.ConstNamedStruct(runType, []llvm.Value{
				run.first.address(ctx.Int8Type(), uintptrType),
				llvm.ConstInt(uintptrType, run.first.size, false),
				llvm.ConstInt(uintptrType, run.stride, false),
				llvm.ConstInt(uintptrType, run.count, false),
			})
		}
		runArrayType, runArray := addGCGlobalRootTable(mod, "runtime.gcGlobalRootRuns", runType, runInitializers)
		defineGCGlobalRootCount(builder, runFuncs[0], len(runs))
		for field, fn := range runFuncs[1:] {
			defineGCGlobalRootField(builder, fn, runArrayType, runArray, runType, field)
		}
	}

	if !rootValues.IsNil() {
		rootValueArray := llvm.AddGlobal(mod, llvm.ArrayType(uintptrType, int(rootWords)), "runtime.gcGlobalRootValueArray")
		rootValueArray.SetInitializer(llvm.ConstNull(rootValueArray.GlobalValueType()))
		rootValueArray.SetLinkage(llvm.InternalLinkage)

		entry := ctx.AddBasicBlock(rootValues, "entry")
		builder.SetInsertPointAtEnd(entry)
		builder.CreateRet(rootValueArray)
	}

	return true
}

// addGCGlobalRootTable adds a constant array of root table entries.
func addGCGlobalRootTable(mod llvm.Module, name string, entryType llvm.Type, entries []llvm.Value) (llvm.Type, llvm.Value) {
	arrayType := llvm.ArrayType(entryType, len(entries))
	array := llvm.AddGlobal(mod, arrayType, name)
	array.SetInitializer(llvm.ConstArray(entryType, entries))
	array.SetGlobalConstant(true)
	array.SetLinkage(llvm.InternalLinkage)
	return arrayType, array
}

// defineGCGlobalRootCount gives fn a body that returns count.
func defineGCGlobalRootCount(builder llvm.Builder, fn llvm.Value, count int) {
	entry := fn.Type().Context().AddBasicBlock(fn, "entry")
	builder.SetInsertPointAtEnd(entry)
	builder.CreateRet(llvm.ConstInt(fn.GlobalValueType().ReturnType(), uint64(count), false))
}

// defineGCGlobalRootField gives fn a body that returns the given field of the
// table entry at the index fn takes.
func defineGCGlobalRootField(builder llvm.Builder, fn llvm.Value, arrayType llvm.Type, array llvm.Value, entryType llvm.Type, field int) {
	ctx := fn.Type().Context()
	entry := ctx.AddBasicBlock(fn, "entry")
	builder.SetInsertPointAtEnd(entry)
	element := builder.CreateInBoundsGEP(arrayType, array, []llvm.Value{
		llvm.ConstInt(ctx.Int32Type(), 0, false),
		fn.FirstParam(),
	}, "")
	addr := builder.CreateStructGEP(entryType, element, field, "")
	builder.CreateRet(builder.CreateLoad(fn.GlobalValueType().ReturnType(), addr, ""))
}

// gcGlobalRootRange is a contiguous range of pointer slots, size bytes at
// offset in global. It never includes padding or non-pointer fields.
type gcGlobalRootRange struct {
	global       llvm.Value
	offset, size uint64
}

// address returns the address of the first slot of the range.
func (r gcGlobalRootRange) address(i8Type, uintptrType llvm.Type) llvm.Value {
	if r.offset == 0 {
		return r.global
	}
	return llvm.ConstGEP(i8Type, r.global, []llvm.Value{
		llvm.ConstInt(uintptrType, r.offset, false),
	})
}

// gcGlobalRootRun stands for count ranges of the same global and size: first,
// and each further one stride bytes after the one before it. The runtime scans
// each of them as a separate range, as if they were in the plain table.
type gcGlobalRootRun struct {
	first         gcGlobalRootRange
	stride, count uint64
}

// gcGlobalRootEncoding knows the size of the entries of both tables.
type gcGlobalRootEncoding struct {
	pointerSize uint64 // the runtime scans a range one word of this size at a time
	rootBytes   uint64 // size of an entry of the plain table
	runBytes    uint64 // size of an entry of the run table
}

// gcGlobalRootMaxPeriod is the largest number of ranges per element (of an
// array, or of a flat struct of equal parts) that makeRuns looks for.
const gcGlobalRootMaxPeriod = 8

// makeRuns encodes the ranges of one global, which are sorted by offset and do
// not touch or overlap, as plain ranges and runs, taking the encoding that
// needs the fewest bytes. An element of a table can have several separate
// ranges of pointers (a struct with a pointer, an integer and a pointer): with
// period p, each run takes every p-th range, so that such a table becomes p
// runs.
func (e gcGlobalRootEncoding) makeRuns(ranges []gcGlobalRootRange) (roots []gcGlobalRootRange, runs []gcGlobalRootRun) {
	bestBytes := ^uint64(0)
	for period := 1; period <= gcGlobalRootMaxPeriod && period <= len(ranges); period++ {
		periodRoots, periodRuns := e.makeRunsWithPeriod(ranges, period)
		bytes := uint64(len(periodRoots))*e.rootBytes + uint64(len(periodRuns))*e.runBytes
		if bytes < bestBytes {
			bestBytes = bytes
			roots, runs = periodRoots, periodRuns
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].offset < roots[j].offset })
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].first.offset < runs[j].first.offset })
	return roots, runs
}

func (e gcGlobalRootEncoding) makeRunsWithPeriod(ranges []gcGlobalRootRange, period int) (roots []gcGlobalRootRange, runs []gcGlobalRootRun) {
	for phase := 0; phase < period; phase++ {
		for i := phase; i < len(ranges); {
			first := ranges[i]
			count := uint64(1)
			stride := uint64(0)
			last := i
			if next := i + period; next < len(ranges) && ranges[next].size == first.size && ranges[next].offset > first.offset {
				stride = ranges[next].offset - first.offset
				for last+period < len(ranges) &&
					ranges[last+period].size == first.size &&
					ranges[last+period].offset == ranges[last].offset+stride {
					last += period
					count++
				}
			}
			if count*e.rootBytes > e.runBytes {
				runs = append(runs, gcGlobalRootRun{first: first, stride: stride, count: count})
				i = last + period
			} else {
				roots = append(roots, first)
				i += period
			}
		}
	}
	return roots, runs
}

// check expands the plain ranges and runs back into the ranges and the words
// the runtime scans, and returns an error unless they are exactly those of
// ranges. The block collector scans each word of each range; the custom and
// Boehm collectors pass each range to a function that scans it.
func (e gcGlobalRootEncoding) check(ranges, roots []gcGlobalRootRange, runs []gcGlobalRootRun) error {
	got := append([]gcGlobalRootRange(nil), roots...)
	for _, run := range runs {
		for k := uint64(0); k < run.count; k++ {
			r := run.first
			r.offset += k * run.stride
			got = append(got, r)
		}
	}
	want := append([]gcGlobalRootRange(nil), ranges...)
	byOffset := func(s []gcGlobalRootRange) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].offset < s[j].offset })
	}
	byOffset(got)
	byOffset(want)
	if len(got) != len(want) {
		return fmt.Errorf("%d ranges instead of %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("range %d is %d+%d instead of %d+%d", i, got[i].offset, got[i].size, want[i].offset, want[i].size)
		}
	}
	gotWords, wantWords := e.words(got), e.words(want)
	if len(gotWords) != len(wantWords) {
		return fmt.Errorf("%d words instead of %d", len(gotWords), len(wantWords))
	}
	for i := range wantWords {
		if gotWords[i] != wantWords[i] {
			return fmt.Errorf("word %d is at %d instead of %d", i, gotWords[i], wantWords[i])
		}
	}
	return nil
}

// words returns the offsets of the words the block collector reads for the
// given ranges (see markGlobals in src/runtime/gc_globals_blocks.go).
func (e gcGlobalRootEncoding) words(ranges []gcGlobalRootRange) []uint64 {
	var words []uint64
	for _, r := range ranges {
		for offset := uint64(0); offset < r.size; offset += e.pointerSize {
			words = append(words, r.offset+offset)
		}
	}
	return words
}

// isRetypedPointerGlobal reports whether a global of type typ, which has no
// pointer type in it, may still hold pointers: an integer one or two pointers
// wide. LLVM can change the type of a global. When a global is only ever copied
// in and out of whole, InstCombine turns those copies into loads and stores of
// an integer of the same size (it does so for copies of four or eight bytes),
// and GlobalOpt then gives the global that integer type: a [2]*T becomes an
// i64. Integers inside aggregates are not treated this way: a table of numbers
// scanned like pointers would keep whatever lies at those addresses alive once
// the heap is large enough to reach them.
func isRetypedPointerGlobal(typ llvm.Type, pointerBits uint64) bool {
	if typ.TypeKind() != llvm.IntegerTypeKind {
		return false
	}
	width := uint64(typ.IntTypeWidth())
	return width == pointerBits || width == 2*pointerBits
}

func appendGCGlobalRootRanges(roots []gcGlobalRootRange, global llvm.Value, typ llvm.Type, targetData llvm.TargetData) []gcGlobalRootRange {
	var offsets []uint64
	offsets = appendGCGlobalRootOffsets(offsets, typ, targetData, 0)
	if len(offsets) == 0 {
		return roots
	}

	pointerSize := uint64(targetData.PointerSize())
	rangeStart := offsets[0]
	rangeEnd := rangeStart + pointerSize
	for _, offset := range offsets[1:] {
		if offset == rangeEnd {
			rangeEnd += pointerSize
			continue
		}
		roots = appendGCGlobalRootRange(roots, global, rangeStart, rangeEnd-rangeStart)
		rangeStart = offset
		rangeEnd = offset + pointerSize
	}
	return appendGCGlobalRootRange(roots, global, rangeStart, rangeEnd-rangeStart)
}

func appendGCGlobalRootRange(roots []gcGlobalRootRange, global llvm.Value, offset, size uint64) []gcGlobalRootRange {
	return append(roots, gcGlobalRootRange{global: global, offset: offset, size: size})
}

func appendGCGlobalRootOffsets(offsets []uint64, typ llvm.Type, targetData llvm.TargetData, baseOffset uint64) []uint64 {
	switch typ.TypeKind() {
	case llvm.PointerTypeKind:
		return append(offsets, baseOffset)
	case llvm.StructTypeKind:
		for i, fieldType := range typ.StructElementTypes() {
			if typeHasPointers(fieldType) {
				fieldOffset := targetData.ElementOffset(typ, i)
				offsets = appendGCGlobalRootOffsets(offsets, fieldType, targetData, baseOffset+fieldOffset)
			}
		}
	case llvm.ArrayTypeKind:
		elemType := typ.ElementType()
		if typeHasPointers(elemType) {
			elemSize := targetData.TypeAllocSize(elemType)
			for i := 0; i < typ.ArrayLength(); i++ {
				offsets = appendGCGlobalRootOffsets(offsets, elemType, targetData, baseOffset+uint64(i)*elemSize)
			}
		}
	}
	return offsets
}

// markParentFunctions traverses all parent function calls (recursively) and
// adds them to the set of marked functions. It only considers function calls:
// any other uses of such a function is ignored.
func markParentFunctions(marked map[llvm.Value]struct{}, fn llvm.Value) {
	worklist := []llvm.Value{fn}
	for len(worklist) != 0 {
		fn := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		for _, use := range getUses(fn) {
			if use.IsACallInst().IsNil() || use.CalledValue() != fn {
				// Not the parent function.
				continue
			}
			parent := use.InstructionParent().Parent()
			if _, ok := marked[parent]; !ok {
				marked[parent] = struct{}{}
				worklist = append(worklist, parent)
			}
		}
	}
}

// blockInCycle returns whether bb lies on a cycle, that is, whether it is
// reachable from itself and so can execute more than once per call. This is
// broader than being a loop header: a merge inside a loop body also qualifies,
// and its slot is overwritten each iteration just the same.
func blockInCycle(bb llvm.BasicBlock) bool {
	// Walk forward from bb and look for an edge back into it.
	seen := map[llvm.BasicBlock]struct{}{}
	worklist := []llvm.BasicBlock{bb}
	for len(worklist) != 0 {
		cur := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		term := cur.LastInstruction()
		if term.IsNil() {
			continue
		}
		for i := 0; i < term.SuccessorsCount(); i++ {
			succ := term.Successor(i)
			if succ == bb {
				return true
			}
			if _, ok := seen[succ]; ok {
				continue
			}
			seen[succ] = struct{}{}
			worklist = append(worklist, succ)
		}
	}
	return false
}

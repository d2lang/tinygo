package transform

import (
	"fmt"
	"sort"
	"strings"

	"tinygo.org/x/go-llvm"
)

// This is somewhat ugly to access through the API.
// https://github.com/llvm/llvm-project/blob/94ebcfd16dac67486bae624f74e1c5c789448bae/llvm/include/llvm/Support/ModRef.h#L62
// https://github.com/llvm/llvm-project/blob/94ebcfd16dac67486bae624f74e1c5c789448bae/llvm/include/llvm/Support/ModRef.h#L87
const shiftExcludeArgMem = 2

// MakeGCStackSlots converts all calls to runtime.trackPointer to explicit
// stores to stack slots that are scannable by the GC.
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

	// All functions that call runtime.alloc needs stack objects.
	trackFuncs := map[llvm.Value]struct{}{}
	markParentFunctions(trackFuncs, alloc)

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

	// Iterate until runtime.trackPointer has no uses left.
	for use := trackPointer.FirstUse(); !use.IsNil(); use = trackPointer.FirstUse() {
		// Pick the first use of runtime.trackPointer.
		call := use.User()
		if call.IsACallInst().IsNil() {
			panic("expected runtime.trackPointer use to be a call")
		}

		// Pick the parent function.
		fn := call.InstructionParent().Parent()

		if _, ok := trackFuncs[fn]; !ok {
			// This function nor any of the functions it calls (recursively)
			// allocate anything from the heap, so it will not trigger a garbage
			// collection cycle. Thus, it does not need to track local pointer
			// values.
			// This is a useful optimization but not as big as you might guess,
			// as described above (it avoids stack objects for ~12% of
			// functions).
			call.EraseFromParentAsInstruction()
			continue
		}

		// Find all calls to runtime.trackPointer in this function.
		var calls []llvm.Value
		var returns []llvm.Value
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
				switch inst.InstructionOpcode() {
				case llvm.Call:
					if inst.CalledValue() == trackPointer {
						calls = append(calls, inst)
					}
				case llvm.Ret:
					returns = append(returns, inst)
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

		if len(pointers) == 0 {
			// This function does not need to keep track of stack pointers.
			continue
		}

		// The stack object has a slot for every pointer.
		fields := make([]llvm.Type, len(pointers))
		for i, ptr := range pointers {
			fields[i] = ptr.Type()
		}
		stackObjectType := ctx.StructType(fields, false)

		// Create the stack object at the function entry, with nothing in it.
		builder.SetInsertPointBefore(fn.EntryBasicBlock().FirstInstruction())
		stackObject := builder.CreateAlloca(stackObjectType, "gc.stackobject")
		builder.CreateStore(llvm.ConstNull(stackObjectType), stackObject)
		builder.CreateCall(exposeType, expose, []llvm.Value{stackObject}, "")

		// Do a store to the stack object after each new pointer that is created.
		for i, ptr := range pointers {
			// Insert the store after the pointer value is created.
			insertionPoint := llvm.NextInstruction(ptr)
			for !insertionPoint.IsAPHINode().IsNil() {
				// PHI nodes are required to be at the start of the block.
				// Insert after the last PHI node.
				insertionPoint = llvm.NextInstruction(insertionPoint)
			}
			builder.SetInsertPointBefore(insertionPoint)

			// Extract a pointer to the appropriate section of the stack object.
			gep := builder.CreateGEP(stackObjectType, stackObject, []llvm.Value{
				llvm.ConstInt(ctx.Int32Type(), 0, false),
				llvm.ConstInt(ctx.Int32Type(), uint64(i), false),
			}, "")

			// Store the pointer into the stack slot.
			builder.CreateStore(ptr, gep)
		}

		// The stack object has to outlive every call that this function
		// makes, as it is what keeps the arguments of those calls alive. A
		// tail call would give up the frame, and the stack object with it,
		// before the callee has run.
		for _, ret := range returns {
			prev := llvm.PrevInstruction(ret)
			if !prev.IsNil() && !prev.IsABitCastInst().IsNil() {
				// A bitcast can appear before a tail call, so skip backwards more.
				prev = llvm.PrevInstruction(prev)
			}
			if !prev.IsNil() && !prev.IsACallInst().IsNil() {
				prev.SetTailCall(false)
			}
		}
	}

	return true
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

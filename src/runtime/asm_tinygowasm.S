.globaltype __stack_pointer, i32

.global  tinygo_getCurrentStackPointer
.hidden  tinygo_getCurrentStackPointer
.type    tinygo_getCurrentStackPointer,@function
tinygo_getCurrentStackPointer: // func getCurrentStackPointer() uintptr
    .functype tinygo_getCurrentStackPointer() -> (i32)
    global.get __stack_pointer
    return
    end_function

.global  tinygo_setCurrentStackPointer
.hidden  tinygo_setCurrentStackPointer
.type    tinygo_setCurrentStackPointer,@function
tinygo_setCurrentStackPointer: // func setCurrentStackPointer(sp uintptr)
    .functype tinygo_setCurrentStackPointer(i32) -> ()
    local.get 0
    global.set __stack_pointer
    end_function

// The garbage collector finds the pointers that a function holds by reading the
// stack, where the compiler makes the function store them in a stack object.
// LLVM cannot see that reader and would remove the stores. So every function
// passes its stack object to this one, which LLVM cannot look into: for all
// LLVM knows, it keeps the address for whatever is called next. It does
// nothing, and wasm-opt removes the calls to it.
//
// The same goes for the other memory that LLVM must not rearrange behind the
// back of the collector: stack variables that may hold a pointer, and new
// allocations that have no slot in a stack object. See MakeGCStackSlots.
.global  tinygo_exposeStackObject
.hidden  tinygo_exposeStackObject
.type    tinygo_exposeStackObject,@function
tinygo_exposeStackObject: // func exposeStackObject(object unsafe.Pointer)
    .functype tinygo_exposeStackObject(i32) -> ()
    end_function

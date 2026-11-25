.globaltype __stack_pointer, i32

.global  tinygo_getCurrentStackPointer
.hidden  tinygo_getCurrentStackPointer
.type    tinygo_getCurrentStackPointer,@function
tinygo_getCurrentStackPointer: // func getCurrentStackPointer() uintptr
    .functype tinygo_getCurrentStackPointer() -> (i32)
    global.get __stack_pointer
    return
    end_function

// The garbage collector finds the pointers that a function holds by reading the
// stack, where the compiler makes the function store them in a stack object.
// LLVM cannot see that reader and would remove the stores. So every function
// passes its stack object to this one, which LLVM cannot look into: for all
// LLVM knows, it keeps the address for whatever is called next. It does
// nothing, and wasm-opt removes the calls to it.
.global  tinygo_exposeStackObject
.hidden  tinygo_exposeStackObject
.type    tinygo_exposeStackObject,@function
tinygo_exposeStackObject: // func exposeStackObject(object unsafe.Pointer)
    .functype tinygo_exposeStackObject(i32) -> ()
    end_function

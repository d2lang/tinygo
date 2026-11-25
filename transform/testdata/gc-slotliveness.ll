target datalayout = "e-m:e-p:32:32-i64:64-n32:64-S128"
target triple = "wasm32-unknown-unknown-wasm"

@global = global ptr null
@unwinding = global i32 0
@table = constant [4 x i8] c"abcd"

declare void @runtime.trackPointer(ptr nocapture readonly)

declare noalias nonnull ptr @runtime.alloc(i32, ptr)

; With these the pass makes the table of global roots, and only then does it
; leave pointers without a slot.
declare i32 @runtime.gcGlobalRootCount()

declare ptr @runtime.gcGlobalRoot(i32)

declare i32 @runtime.gcGlobalRootSize(i32)

; Nothing is known about these, so they may collect.
declare void @collect()

declare void @collectWith(ptr)

declare ptr @collectAndReturn(ptr)

declare { ptr, i32 } @makeString()

declare void @fill(ptr)

declare void @llvm.memcpy.p0.p0.i32(ptr, ptr, i32, i1)

declare i32 @setjmp(ptr) returns_twice

; This one cannot collect.
define i32 @leaf(ptr readonly captures(none) %p) {
  %v = load i32, ptr %p
  ret i32 %v
}

; A function that returns a new object, as LLVM has worked out.
define noalias ptr @newObject() {
  %p = call ptr @runtime.alloc(i32 8, ptr null)
  call void @runtime.trackPointer(ptr %p)
  ret ptr %p
}

; %kept is used after a call that may collect, %dropped only before it.
; %dropped has no slot, so it is exposed: for LLVM it is no longer memory that
; only this function knows.
define i32 @liveAcrossCall() {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  store i32 1, ptr %dropped
  call void @collect()
  %v = load i32, ptr %kept
  ret i32 %v
}

; An argument of a call that may collect is in use during the call: the callee
; does not root its parameters. An argument of a call that cannot collect is not.
define i32 @liveAtCall() {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  %v = call i32 @leaf(ptr %dropped)
  call void @collectWith(ptr %kept)
  ret i32 %v
}

; No slot, no stack object, nothing to clear. An allocation that is not used
; at all is not exposed either, so that LLVM can remove it.
define void @nothingLive() {
  %unused = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %unused)
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  store i32 1, ptr %dropped
  ret void
}

; A call to a function that cannot collect is not a reason for a slot.
define i32 @acrossLeafCall() {
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  %a = call i32 @leaf(ptr %dropped)
  %b = load i32, ptr %dropped
  %c = add i32 %a, %b
  ret i32 %c
}

; A call through a function pointer may collect.
define i32 @acrossIndirectCall(ptr %fn) {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  call void %fn()
  %v = load i32, ptr %kept
  ret i32 %v
}

; runtime.KeepAlive is inline assembly that takes the pointer. It is treated
; like a call through a function pointer.
define void @keepAlive() {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  call void @collect()
  call void asm sideeffect "", "r"(ptr %kept)
  ret void
}

; H3. An interior pointer keeps the object in use.
define i32 @interiorPointer() {
  %kept = call ptr @runtime.alloc(i32 8, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %field = getelementptr i8, ptr %kept, i32 4
  call void @collect()
  %v = load i32, ptr %field
  ret i32 %v
}

; H3. So does the pointer as an integer, and what is computed from it. The
; pointer that is made from the integer is itself not in use at a call.
define i32 @integerRoundTrip() {
  %kept = call ptr @runtime.alloc(i32 8, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %int = ptrtoint ptr %kept to i32
  call void @collect()
  %next = add i32 %int, 4
  %field = inttoptr i32 %next to ptr
  call void @runtime.trackPointer(ptr %field)
  %v = load i32, ptr %field
  ret i32 %v
}

; H3. The pointer of a string that is only asked for its length afterwards is
; not in use.
define i32 @stringLength() {
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  call void @runtime.trackPointer(ptr %s.data)
  call void @collect()
  %s.len = extractvalue { ptr, i32 } %s, 1
  ret i32 %s.len
}

; H3. Here the tracked value is not used again, but the same field is taken out
; of the string a second time after the call.
define i8 @stringData() {
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  call void @runtime.trackPointer(ptr %s.data)
  call void @collect()
  %again = extractvalue { ptr, i32 } %s, 0
  %v = load i8, ptr %again
  ret i8 %v
}

; H3. The compiler repacks the result of a call and tracks the pointer it takes
; out of the copy, after the check for a panic. The slot is for the pointer as
; the call returned it, and is filled right after the call.
define i8 @repackedString() {
entry:
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  %copy.0 = insertvalue { ptr, i32 } poison, ptr %s.data, 0
  %s.len = extractvalue { ptr, i32 } %s, 1
  %copy = insertvalue { ptr, i32 } %copy.0, i32 %s.len, 1
  %unwinding = load i32, ptr @unwinding
  %ok = icmp eq i32 %unwinding, 0
  br i1 %ok, label %continue, label %unwind

continue:
  %data = extractvalue { ptr, i32 } %copy, 0
  call void @runtime.trackPointer(ptr %data)
  call void @collect()
  %v = load i8, ptr %data
  ret i8 %v

unwind:
  ret i8 undef
}

; H3. A pointer inside an aggregate that is passed to a call.
define void @aggregateArgument(i32 %len) {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %s.0 = insertvalue { ptr, i32 } poison, ptr %kept, 0
  %s = insertvalue { ptr, i32 } %s.0, i32 %len, 1
  call void @collectWithString({ ptr, i32 } %s)
  ret void
}

declare void @collectWithString({ ptr, i32 })

; H3. A value merged from two pointers keeps both in use: each on its own path.
define i32 @mergedPointers(i1 %condition) {
entry:
  br i1 %condition, label %left, label %right

left:
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %a)
  br label %join

right:
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %b)
  br label %join

join:
  %merged = phi ptr [ %a, %left ], [ %b, %right ]
  call void @collect()
  %v = load i32, ptr %merged
  ret i32 %v
}

; H3. The same with a select. %c is not selected and not used later.
define i32 @selectedPointers(i1 %condition) {
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %a)
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %b)
  %c = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %c)
  store i32 0, ptr %c
  %selected = select i1 %condition, ptr %a, ptr %b
  call void @collect()
  %v = load i32, ptr %selected
  ret i32 %v
}

; H3. The value of %previous is the object of the iteration before. Its slot
; is its own: the slot of %current is overwritten before %previous is used.
; %current is in use until the end of the block, where the phi takes it, and
; a call comes before that.
define void @loopCarried(i1 %repeat) {
entry:
  br label %loop

loop:
  %previous = phi ptr [ null, %entry ], [ %current, %loop ]
  call void @runtime.trackPointer(ptr %previous)
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %current)
  call void @collectWith(ptr %previous)
  br i1 %repeat, label %loop, label %end

end:
  ret void
}

; H3. Nothing is in use at a call here: %previous is read before the
; allocation, and %current goes round the loop without passing one.
define i32 @loopNotLive(i1 %repeat) {
entry:
  br label %loop

loop:
  %previous = phi ptr [ @global, %entry ], [ %current, %loop ]
  call void @runtime.trackPointer(ptr %previous)
  %v = load i32, ptr %previous
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %current)
  br i1 %repeat, label %loop, label %end

end:
  ret i32 %v
}

; H3. A phi in a loop that the compiler did not track gets a slot all the same.
define i32 @loopUntrackedPhi(i1 %repeat) {
entry:
  %first = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %first)
  br label %loop

loop:
  %previous = phi ptr [ %first, %entry ], [ %current, %loop ]
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %current)
  %v = load i32, ptr %previous
  br i1 %repeat, label %loop, label %end

end:
  ret i32 %v
}

; H3. A pointer that was loaded and not tracked gets a slot if it is in use at
; a call.
define i32 @untrackedLoad() {
  %kept = load ptr, ptr @global
  %dropped = load ptr, ptr @global
  %a = load i32, ptr %dropped
  call void @collect()
  %b = load i32, ptr %kept
  %c = add i32 %a, %b
  ret i32 %c
}

; H4. After a call, the code that the compiler emits tests whether a panic is
; unwinding the stack and if so runs the deferred calls, which may collect and
; which use %deferred. %result is only used where the function returns.
define ptr @unwindCheck() {
entry:
  %deferred = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %deferred)
  %result = call ptr @collectAndReturn(ptr null)
  call void @runtime.trackPointer(ptr %result)
  %unwinding = load i32, ptr @unwinding
  %ok = icmp eq i32 %unwinding, 0
  br i1 %ok, label %continue, label %unwind

continue:
  ret ptr %result

unwind:
  call void @collectWith(ptr %deferred)
  ret ptr null
}

; H4. The stack object has to be there during the call, so the call is no
; longer marked as a tail call.
define ptr @tailCall() {
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %kept)
  %result = tail call ptr @collectAndReturn(ptr %kept)
  ret ptr %result
}

; H4. Without anything exposed the mark can stay.
define void @tailCallWithoutSlots() {
  tail call void @collect()
  ret void
}

; H4. A function that can be returned into twice keeps all its slots: this
; analysis does not know where control comes back.
define i32 @returnsTwice(ptr %buf) {
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %a)
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %b)
  %again = call i32 @setjmp(ptr %buf)
  %v = load i32, ptr %b
  ret i32 %v
}

; H1. The result of a function that returns a new object is exposed like an
; allocation.
define i32 @newObjectWithoutSlot() {
  %dropped = call ptr @newObject()
  call void @runtime.trackPointer(ptr %dropped)
  %v = load i32, ptr %dropped
  call void @collect()
  ret i32 %v
}

; H2. A pointer that is kept in a stack variable across a call. The variable
; is exposed, so LLVM cannot turn it into a value later. The stack is scanned.
define i32 @storedOnStack() {
  %variable = alloca ptr
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  store ptr %dropped, ptr %variable
  call void @collect()
  %reloaded = load ptr, ptr %variable
  call void @runtime.trackPointer(ptr %reloaded)
  %v = load i32, ptr %reloaded
  ret i32 %v
}

; H2. A function that is given the address of a stack variable may store a
; pointer there, and may be inlined later.
define i32 @filledByCallee() {
  %variable = alloca ptr
  call void @fill(ptr %variable)
  call void @collect()
  %reloaded = load ptr, ptr %variable
  call void @runtime.trackPointer(ptr %reloaded)
  %v = load i32, ptr %reloaded
  ret i32 %v
}

; H2. A copy from somewhere on the heap may bring pointers.
define i32 @copiedToStack(ptr %from) {
  %variable = alloca { ptr, i32 }
  call void @llvm.memcpy.p0.p0.i32(ptr %variable, ptr %from, i32 8, i1 false)
  call void @collect()
  %reloaded = load ptr, ptr %variable
  call void @runtime.trackPointer(ptr %reloaded)
  %v = load i32, ptr %reloaded
  ret i32 %v
}

; H2. What a parameter points to is rooted by the caller. A stack variable
; that holds nothing else is left alone.
define i32 @parameterOnStack(ptr %p) {
  %variable = alloca ptr
  store ptr %p, ptr %variable
  call void @collect()
  %reloaded = load ptr, ptr %variable
  call void @runtime.trackPointer(ptr %reloaded)
  %v = load i32, ptr %reloaded
  ret i32 %v
}

; H2. This variable holds a pointer, but nothing can collect between the
; store and the last use of the variable.
define i32 @stackNotAtCollection() {
  %variable = alloca ptr
  call void @collect()
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @runtime.trackPointer(ptr %dropped)
  store ptr %dropped, ptr %variable
  %v = call i32 @leaf(ptr %variable)
  ret i32 %v
}

; H2. These stack variables cannot hold a pointer into the heap: one only has
; integers and the address of a global stored in it and is read by a function
; that does not keep its address, the other is a copy of constant data.
define i32 @stackWithoutPointers(i32 %x) {
  %numbers = alloca { i32, ptr }
  %text = alloca [4 x i8]
  store i32 %x, ptr %numbers
  %second = getelementptr { i32, ptr }, ptr %numbers, i32 0, i32 1
  store ptr @global, ptr %second
  call void @llvm.memcpy.p0.p0.i32(ptr %text, ptr @table, i32 4, i1 false)
  call void @collect()
  %a = call i32 @leaf(ptr %numbers)
  %b = call i32 @leaf(ptr %text)
  %c = add i32 %a, %b
  ret i32 %c
}

declare void @takesInteger(i32)

; LLVM copies two pointers as one i64. With the variable they were copied to
; gone, that integer is all that has them during the call.
define i32 @integerCopy(ptr %from) {
  %both = load i64, ptr %from
  call void @collect()
  %low = trunc i64 %both to i32
  %p = inttoptr i32 %low to ptr
  %v = load i32, ptr %p
  ret i32 %v
}

; The integer is never a pointer here: it goes back to memory as it is.
define void @integerCopyStored(ptr %from, ptr %to) {
  %both = load i64, ptr %from
  call void @collect()
  store i64 %both, ptr %to
  ret void
}

; The half that is used is taken before the call. The slot is that of the
; load: it is rooted where it is made.
define i32 @integerCopyHalf(ptr %from) {
  %both = load i64, ptr %from
  %shifted = lshr i64 %both, 32
  %high = trunc i64 %shifted to i32
  call void @collect()
  %p = inttoptr i32 %high to ptr
  %v = load i32, ptr %p
  ret i32 %v
}

; An address can be computed from the integer without an inttoptr.
define i32 @integerAddress(ptr %from) {
  %n = load i32, ptr %from
  call void @collect()
  %p = getelementptr i8, ptr null, i32 %n
  %v = load i32, ptr %p
  ret i32 %v
}

; What an address is computed from is a pointer, however it is computed.
define i32 @integerSum(ptr %from) {
  %n = load i32, ptr %from
  %next = add i32 %n, 4
  call void @collect()
  %p = inttoptr i32 %next to ptr
  %v = load i32, ptr %p
  ret i32 %v
}

; An integer that is passed on or returned after the call may be a pointer to
; whoever gets it.
define i32 @integerPassedOn(ptr %from) {
  %n = load i32, ptr %from
  %m = call i32 @leaf(ptr %from)
  call void @collect()
  call void @takesInteger(i32 %n)
  ret i32 %m
}

; An integer that is only calculated with or compared is not a pointer for
; anybody, and neither is one that is passed on before anything collects.
define i32 @integerNotDelivered(ptr %from) {
  %n = load i32, ptr %from
  %m = load i32, ptr %from
  call void @takesInteger(i32 %m)
  call void @collect()
  %sum = add i32 %n, 1
  %big = icmp ugt i32 %n, 10
  %r = select i1 %big, i32 %sum, i32 0
  ret i32 %r
}

; An integer that is carried round a loop outlives the slot of the load.
define void @integerLoop(ptr %from, ptr %to, i1 %repeat) {
entry:
  br label %loop

loop:
  %previous = phi i32 [ 0, %entry ], [ %current, %loop ]
  %current = load i32, ptr %from
  call void @collect()
  store i32 %previous, ptr %to
  br i1 %repeat, label %loop, label %end

end:
  ret void
}

; A stack variable that gets half of such an integer has to stay in memory.
define i32 @integerOnStack(ptr %from) {
  %variable = alloca i32
  %both = load i64, ptr %from
  %low = trunc i64 %both to i32
  store i32 %low, ptr %variable
  call void @collect()
  %v = call i32 @leaf(ptr %variable)
  ret i32 %v
}

; Where the pass cannot follow what happens it keeps every such integer that
; is delivered, as it keeps every tracked pointer.
define i32 @integerCopyReturnsTwice(ptr %from, ptr %buf) {
  %both = load i64, ptr %from
  %again = call i32 @setjmp(ptr %buf)
  call void @collect()
  %low = trunc i64 %both to i32
  %p = inttoptr i32 %low to ptr
  %v = load i32, ptr %p
  ret i32 %v
}

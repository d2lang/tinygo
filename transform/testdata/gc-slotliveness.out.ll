target datalayout = "e-m:e-p:32:32-i64:64-n32:64-S128"
target triple = "wasm32-unknown-unknown-wasm"

@global = global ptr null
@unwinding = global i32 0
@table = constant [4 x i8] c"abcd"
@runtime.gcGlobalRoots = internal constant [2 x { ptr, i32 }] [{ ptr, i32 } { ptr @global, i32 4 }, { ptr, i32 } { ptr @unwinding, i32 4 }]

declare void @runtime.trackPointer(ptr nocapture readonly)

declare noalias nonnull ptr @runtime.alloc(i32, ptr)

define i32 @runtime.gcGlobalRootCount() {
entry:
  ret i32 2
}

define ptr @runtime.gcGlobalRoot(i32 %0) {
entry:
  %1 = getelementptr inbounds [2 x { ptr, i32 }], ptr @runtime.gcGlobalRoots, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32 }, ptr %1, i32 0, i32 0
  %3 = load ptr, ptr %2, align 4
  ret ptr %3
}

define i32 @runtime.gcGlobalRootSize(i32 %0) {
entry:
  %1 = getelementptr inbounds [2 x { ptr, i32 }], ptr @runtime.gcGlobalRoots, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32 }, ptr %1, i32 0, i32 1
  %3 = load i32, ptr %2, align 4
  ret i32 %3
}

declare void @collect()

declare void @collectWith(ptr)

declare ptr @collectAndReturn(ptr)

declare { ptr, i32 } @makeString()

declare void @fill(ptr)

; Function Attrs: nocallback nofree nounwind willreturn memory(argmem: readwrite)
declare void @llvm.memcpy.p0.p0.i32(ptr noalias nocapture writeonly, ptr noalias nocapture readonly, i32, i1 immarg) #0

; Function Attrs: returns_twice
declare i32 @setjmp(ptr) #1

define i32 @leaf(ptr nocapture readonly %p) {
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define noalias ptr @newObject() {
  %p = call ptr @runtime.alloc(i32 8, ptr null)
  call void @tinygo_exposeStackObject(ptr %p)
  ret ptr %p
}

define i32 @liveAcrossCall() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  store i32 1, ptr %dropped, align 4
  call void @collect()
  %v = load i32, ptr %kept, align 4
  ret i32 %v
}

define i32 @liveAtCall() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  %v = call i32 @leaf(ptr %dropped)
  call void @collectWith(ptr %kept)
  ret i32 %v
}

define void @nothingLive() {
  %unused = call ptr @runtime.alloc(i32 4, ptr null)
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  store i32 1, ptr %dropped, align 4
  ret void
}

define i32 @acrossLeafCall() {
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  %a = call i32 @leaf(ptr %dropped)
  %b = load i32, ptr %dropped, align 4
  %c = add i32 %a, %b
  ret i32 %c
}

define i32 @acrossIndirectCall(ptr %fn) {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  call void %fn()
  %v = load i32, ptr %kept, align 4
  ret i32 %v
}

define void @keepAlive() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  call void @collect()
  call void asm sideeffect "", "r"(ptr %kept)
  ret void
}

define i32 @interiorPointer() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 8, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %field = getelementptr i8, ptr %kept, i32 4
  call void @collect()
  %v = load i32, ptr %field, align 4
  ret i32 %v
}

define i32 @integerRoundTrip() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 8, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %int = ptrtoint ptr %kept to i32
  call void @collect()
  %next = add i32 %int, 4
  %field = inttoptr i32 %next to ptr
  %v = load i32, ptr %field, align 4
  ret i32 %v
}

define i32 @stringLength() {
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  call void @collect()
  %s.len = extractvalue { ptr, i32 } %s, 1
  ret i32 %s.len
}

define i8 @stringData() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %s.data, ptr %1, align 4
  call void @collect()
  %again = extractvalue { ptr, i32 } %s, 0
  %v = load i8, ptr %again, align 1
  ret i8 %v
}

define i8 @repackedString() {
entry:
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %s = call { ptr, i32 } @makeString()
  %s.data = extractvalue { ptr, i32 } %s, 0
  %0 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %s.data, ptr %0, align 4
  %copy.0 = insertvalue { ptr, i32 } poison, ptr %s.data, 0
  %s.len = extractvalue { ptr, i32 } %s, 1
  %copy = insertvalue { ptr, i32 } %copy.0, i32 %s.len, 1
  %unwinding = load i32, ptr @unwinding, align 4
  %ok = icmp eq i32 %unwinding, 0
  br i1 %ok, label %continue, label %unwind

continue:                                         ; preds = %entry
  %data = extractvalue { ptr, i32 } %copy, 0
  call void @collect()
  %v = load i8, ptr %data, align 1
  ret i8 %v

unwind:                                           ; preds = %entry
  ret i8 undef
}

define void @aggregateArgument(i32 %len) {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %s.0 = insertvalue { ptr, i32 } poison, ptr %kept, 0
  %s = insertvalue { ptr, i32 } %s.0, i32 %len, 1
  call void @collectWithString({ ptr, i32 } %s)
  ret void
}

declare void @collectWithString({ ptr, i32 })

define i32 @mergedPointers(i1 %condition) {
entry:
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  br i1 %condition, label %left, label %right

left:                                             ; preds = %entry
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  %0 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %a, ptr %0, align 4
  br label %join

right:                                            ; preds = %entry
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %b, ptr %1, align 4
  br label %join

join:                                             ; preds = %right, %left
  %merged = phi ptr [ %a, %left ], [ %b, %right ]
  call void @collect()
  %v = load i32, ptr %merged, align 4
  ret i32 %v
}

define i32 @selectedPointers(i1 %condition) {
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %a, ptr %1, align 4
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  %2 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %b, ptr %2, align 4
  %c = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %c)
  store i32 0, ptr %c, align 4
  %selected = select i1 %condition, ptr %a, ptr %b
  call void @collect()
  %v = load i32, ptr %selected, align 4
  ret i32 %v
}

define void @loopCarried(i1 %repeat) {
entry:
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  br label %loop

loop:                                             ; preds = %loop, %entry
  %previous = phi ptr [ null, %entry ], [ %current, %loop ]
  %0 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %previous, ptr %0, align 4
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %current, ptr %1, align 4
  call void @collectWith(ptr %previous)
  br i1 %repeat, label %loop, label %end

end:                                              ; preds = %loop
  ret void
}

define i32 @loopNotLive(i1 %repeat) {
entry:
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  br label %loop

loop:                                             ; preds = %loop, %entry
  %previous = phi ptr [ @global, %entry ], [ %current, %loop ]
  %v = load i32, ptr %previous, align 4
  %0 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %v, ptr %0, align 4
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %current)
  br i1 %repeat, label %loop, label %end

end:                                              ; preds = %loop
  ret i32 %v
}

define i32 @loopUntrackedPhi(i1 %repeat) {
entry:
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %first = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %first)
  br label %loop

loop:                                             ; preds = %loop, %entry
  %previous = phi ptr [ %first, %entry ], [ %current, %loop ]
  %0 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %previous, ptr %0, align 4
  %current = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %current)
  %v = load i32, ptr %previous, align 4
  br i1 %repeat, label %loop, label %end

end:                                              ; preds = %loop
  ret i32 %v
}

define i32 @untrackedLoad() {
  %gc.stackobject = alloca { ptr, i32 }, align 8
  store { ptr, i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = load ptr, ptr @global, align 4
  %1 = getelementptr { ptr, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %dropped = load ptr, ptr @global, align 4
  %a = load i32, ptr %dropped, align 4
  %2 = getelementptr { ptr, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store i32 %a, ptr %2, align 4
  call void @collect()
  %b = load i32, ptr %kept, align 4
  %c = add i32 %a, %b
  ret i32 %c
}

define ptr @unwindCheck() {
entry:
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %deferred = call ptr @runtime.alloc(i32 4, ptr null)
  %0 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %deferred, ptr %0, align 4
  %result = call ptr @collectAndReturn(ptr null)
  %unwinding = load i32, ptr @unwinding, align 4
  %ok = icmp eq i32 %unwinding, 0
  br i1 %ok, label %continue, label %unwind

continue:                                         ; preds = %entry
  ret ptr %result

unwind:                                           ; preds = %entry
  call void @collectWith(ptr %deferred)
  ret ptr null
}

define ptr @tailCall() {
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %kept = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %kept, ptr %1, align 4
  %result = call ptr @collectAndReturn(ptr %kept)
  ret ptr %result
}

define void @tailCallWithoutSlots() {
  tail call void @collect()
  ret void
}

define i32 @returnsTwice(ptr %buf) {
  %gc.stackobject = alloca { ptr, ptr, i32 }, align 8
  store { ptr, ptr, i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %a = call ptr @runtime.alloc(i32 4, ptr null)
  %1 = getelementptr { ptr, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %a, ptr %1, align 4
  %b = call ptr @runtime.alloc(i32 4, ptr null)
  %2 = getelementptr { ptr, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %b, ptr %2, align 4
  %again = call i32 @setjmp(ptr %buf)
  %v = load i32, ptr %b, align 4
  %3 = getelementptr { ptr, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 2
  store i32 %v, ptr %3, align 4
  ret i32 %v
}

define i32 @newObjectWithoutSlot() {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %dropped = call ptr @newObject()
  call void @tinygo_exposeStackObject(ptr %dropped)
  %v = load i32, ptr %dropped, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %v, ptr %1, align 4
  call void @collect()
  ret i32 %v
}

define i32 @storedOnStack() {
  %variable = alloca ptr, align 4
  call void @tinygo_exposeStackObject(ptr %variable)
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  store ptr %dropped, ptr %variable, align 4
  call void @collect()
  %reloaded = load ptr, ptr %variable, align 4
  %v = load i32, ptr %reloaded, align 4
  ret i32 %v
}

define i32 @filledByCallee() {
  %variable = alloca ptr, align 4
  call void @tinygo_exposeStackObject(ptr %variable)
  call void @fill(ptr %variable)
  call void @collect()
  %reloaded = load ptr, ptr %variable, align 4
  %v = load i32, ptr %reloaded, align 4
  ret i32 %v
}

define i32 @copiedToStack(ptr %from) {
  %variable = alloca { ptr, i32 }, align 8
  call void @tinygo_exposeStackObject(ptr %variable)
  call void @llvm.memcpy.p0.p0.i32(ptr %variable, ptr %from, i32 8, i1 false)
  call void @collect()
  %reloaded = load ptr, ptr %variable, align 4
  %v = load i32, ptr %reloaded, align 4
  ret i32 %v
}

define i32 @parameterOnStack(ptr %p) {
  %variable = alloca ptr, align 4
  store ptr %p, ptr %variable, align 4
  call void @collect()
  %reloaded = load ptr, ptr %variable, align 4
  %v = load i32, ptr %reloaded, align 4
  ret i32 %v
}

define i32 @stackNotAtCollection() {
  %variable = alloca ptr, align 4
  call void @collect()
  %dropped = call ptr @runtime.alloc(i32 4, ptr null)
  call void @tinygo_exposeStackObject(ptr %dropped)
  store ptr %dropped, ptr %variable, align 4
  %v = call i32 @leaf(ptr %variable)
  ret i32 %v
}

define i32 @stackWithoutPointers(i32 %x) {
  %numbers = alloca { i32, ptr }, align 8
  %text = alloca [4 x i8], align 1
  store i32 %x, ptr %numbers, align 4
  %second = getelementptr { i32, ptr }, ptr %numbers, i32 0, i32 1
  store ptr @global, ptr %second, align 4
  call void @llvm.memcpy.p0.p0.i32(ptr %text, ptr @table, i32 4, i1 false)
  call void @collect()
  %a = call i32 @leaf(ptr %numbers)
  %b = call i32 @leaf(ptr %text)
  %c = add i32 %a, %b
  ret i32 %c
}

declare void @takesInteger(i32)

define i32 @integerCopy(ptr %from) {
  %gc.stackobject = alloca { i64 }, align 8
  store { i64 } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %both = load i64, ptr %from, align 8
  %1 = getelementptr { i64 }, ptr %gc.stackobject, i32 0, i32 0
  store i64 %both, ptr %1, align 8
  call void @collect()
  %low = trunc i64 %both to i32
  %p = inttoptr i32 %low to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define void @integerCopyStored(ptr %from, ptr %to) {
  %gc.stackobject = alloca { i64 }, align 8
  store { i64 } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %both = load i64, ptr %from, align 8
  %1 = getelementptr { i64 }, ptr %gc.stackobject, i32 0, i32 0
  store i64 %both, ptr %1, align 8
  call void @collect()
  store i64 %both, ptr %to, align 8
  ret void
}

define i32 @integerCopyHalf(ptr %from) {
  %gc.stackobject = alloca { i64 }, align 8
  store { i64 } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %both = load i64, ptr %from, align 8
  %1 = getelementptr { i64 }, ptr %gc.stackobject, i32 0, i32 0
  store i64 %both, ptr %1, align 8
  %shifted = lshr i64 %both, 32
  %high = trunc i64 %shifted to i32
  call void @collect()
  %p = inttoptr i32 %high to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define i32 @integerAddress(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  call void @collect()
  %p = getelementptr i8, ptr null, i32 %n
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define i32 @integerSum(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  %next = add i32 %n, 4
  call void @collect()
  %p = inttoptr i32 %next to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define i32 @integerPassedOn(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  %m = call i32 @leaf(ptr %from)
  call void @collect()
  call void @takesInteger(i32 %n)
  ret i32 %m
}

define i32 @integerCalculated(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  %m = load i32, ptr %from, align 4
  call void @takesInteger(i32 %m)
  call void @collect()
  %sum = add i32 %n, 1
  %big = icmp ugt i32 %n, 10
  %r = select i1 %big, i32 %sum, i32 0
  ret i32 %r
}

define i1 @integerIdentity(ptr %a, ptr %b) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %old = load i32, ptr %a, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %old, ptr %1, align 4
  store ptr null, ptr %a, align 4
  call void @collect()
  %new = load i32, ptr %b, align 4
  %same = icmp eq i32 %old, %new
  ret i1 %same
}

define void @integerOffsetStored(ptr %from, ptr %to) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  call void @collect()
  %m = add i32 %n, 4
  store i32 %m, ptr %to, align 4
  ret void
}

define i32 @integerNoPointers(ptr %from) {
  %n = load i32, ptr %from, align 4, !tinygo.noptr !0
  call void @collect()
  %sum = add i32 %n, 1
  ret i32 %sum
}

define i32 @integerNoPointersAddress(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4, !tinygo.noptr !0
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  call void @collect()
  %p = inttoptr i32 %n to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

declare void @collectWithInteger(i32)

define void @integerArgument(ptr %from) {
  %n = load i32, ptr %from, align 4
  call void @collectWithInteger(i32 %n)
  ret void
}

define i32 @doubleAddress(ptr %from) {
  %gc.stackobject = alloca { double }, align 8
  store { double } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %d = load double, ptr %from, align 8
  %1 = getelementptr { double }, ptr %gc.stackobject, i32 0, i32 0
  store double %d, ptr %1, align 8
  call void @collect()
  %bits = bitcast double %d to i64
  %low = trunc i64 %bits to i32
  %p = inttoptr i32 %low to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define i32 @pointerAhead(ptr %from, i32 %n) {
entry:
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %base = load ptr, ptr %from, align 4
  %0 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %base, ptr %0, align 4
  %first = getelementptr i8, ptr %base, i32 4
  br label %loop

loop:                                             ; preds = %loop, %entry
  %ahead = phi ptr [ %first, %entry ], [ %next, %loop ]
  %i = phi i32 [ 0, %entry ], [ %i.next, %loop ]
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %ahead, ptr %1, align 4
  call void @collect()
  %elem = getelementptr i8, ptr %ahead, i32 -4
  %v = load i32, ptr %elem, align 4
  %next = getelementptr i8, ptr %ahead, i32 4
  %i.next = add i32 %i, 1
  %done = icmp eq i32 %i.next, %n
  br i1 %done, label %end, label %loop

end:                                              ; preds = %loop
  ret i32 %v
}

define i32 @pointerStepsFromStart() {
entry:
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %base = call ptr @runtime.alloc(i32 16, ptr null)
  %0 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %base, ptr %0, align 4
  br label %loop

loop:                                             ; preds = %loop, %entry
  %q = phi ptr [ %base, %entry ], [ %next, %loop ]
  %i = phi i32 [ 0, %entry ], [ %i.next, %loop ]
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %q, ptr %1, align 4
  call void @collect()
  %behind = getelementptr i8, ptr %q, i32 -12
  %v = load i32, ptr %behind, align 4
  %next = getelementptr i8, ptr %q, i32 4
  %i.next = add i32 %i, 1
  %done = icmp eq i32 %i.next, 8
  br i1 %done, label %end, label %loop

end:                                              ; preds = %loop
  ret i32 %v
}

define i32 @integerStepsFromLoad(ptr %from) {
entry:
  %gc.stackobject = alloca { i32, i32 }, align 8
  store { i32, i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %x = load i32, ptr %from, align 4
  %0 = getelementptr { i32, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %x, ptr %0, align 4
  br label %loop

loop:                                             ; preds = %loop, %entry
  %ip = phi i32 [ %x, %entry ], [ %ip.next, %loop ]
  %1 = getelementptr { i32, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store i32 %ip, ptr %1, align 4
  call void @collect()
  %addr = add i32 %ip, -4
  %p = inttoptr i32 %addr to ptr
  %v = load i32, ptr %p, align 4
  %ip.next = add i32 %ip, 4
  %done = icmp eq i32 %v, 0
  br i1 %done, label %end, label %loop

end:                                              ; preds = %loop
  ret i32 %v
}

define i32 @trackedEnd(i1 %c, ptr %other) {
  %gc.stackobject = alloca { ptr, ptr }, align 8
  store { ptr, ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %base = call ptr @runtime.alloc(i32 16, ptr null)
  %1 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %base, ptr %1, align 4
  %end = getelementptr i8, ptr %base, i32 16
  %p = select i1 %c, ptr %end, ptr %other
  %2 = getelementptr { ptr, ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %p, ptr %2, align 4
  call void @collect()
  %last = getelementptr i8, ptr %p, i32 -4
  %v = load i32, ptr %last, align 4
  ret i32 %v
}

declare { i32, double } @makePair()

define i32 @doubleFieldAddress(ptr %from) {
  %gc.stackobject = alloca { double }, align 8
  store { double } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %pair = load { i32, double }, ptr %from, align 8
  %1 = extractvalue { i32, double } %pair, 1
  %2 = getelementptr { double }, ptr %gc.stackobject, i32 0, i32 0
  store double %1, ptr %2, align 8
  call void @collect()
  %d = extractvalue { i32, double } %pair, 1
  %bits = bitcast double %d to i64
  %low = trunc i64 %bits to i32
  %p = inttoptr i32 %low to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

define i32 @floatRoundTrip(ptr %from) {
  %gc.stackobject = alloca { i32 }, align 8
  store { i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %n = load i32, ptr %from, align 4
  %1 = getelementptr { i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %n, ptr %1, align 4
  %d = uitofp i32 %n to double
  call void @collect()
  %m = fptoui double %d to i32
  %p = inttoptr i32 %m to ptr
  %v = load i32, ptr %p, align 4
  ret i32 %v
}

declare <2 x ptr> @twoPointers()

define i32 @vectorResult() {
  %gc.stackobject = alloca { <2 x ptr>, i32 }, align 8
  store { <2 x ptr>, i32 } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %pair = call <2 x ptr> @twoPointers()
  %1 = getelementptr { <2 x ptr>, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store <2 x ptr> %pair, ptr %1, align 8
  call void @collect()
  %p = extractelement <2 x ptr> %pair, i32 0
  %v = load i32, ptr %p, align 4
  %2 = getelementptr { <2 x ptr>, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store i32 %v, ptr %2, align 4
  ret i32 %v
}

define void @listWalk(ptr %head) {
entry:
  %gc.stackobject = alloca { ptr }, align 8
  store { ptr } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %first = load ptr, ptr %head, align 4
  br label %loop

loop:                                             ; preds = %loop, %entry
  %node = phi ptr [ %first, %entry ], [ %next, %loop ]
  %0 = getelementptr { ptr }, ptr %gc.stackobject, i32 0, i32 0
  store ptr %node, ptr %0, align 4
  call void @collectWith(ptr %node)
  %next = load ptr, ptr %node, align 4
  %end = icmp eq ptr %next, null
  br i1 %end, label %done, label %loop

done:                                             ; preds = %loop
  ret void
}

define void @integerLoop(ptr %from, ptr %to, i1 %repeat) {
entry:
  %gc.stackobject = alloca { i32, i32 }, align 8
  store { i32, i32 } zeroinitializer, ptr %gc.stackobject, align 4
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  br label %loop

loop:                                             ; preds = %loop, %entry
  %previous = phi i32 [ 0, %entry ], [ %current, %loop ]
  %0 = getelementptr { i32, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i32 %previous, ptr %0, align 4
  %current = load i32, ptr %from, align 4
  %1 = getelementptr { i32, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store i32 %current, ptr %1, align 4
  call void @collect()
  store i32 %previous, ptr %to, align 4
  br i1 %repeat, label %loop, label %end

end:                                              ; preds = %loop
  ret void
}

define i32 @integerOnStack(ptr %from) {
  %variable = alloca i32, align 4
  call void @tinygo_exposeStackObject(ptr %variable)
  %both = load i64, ptr %from, align 8
  %low = trunc i64 %both to i32
  store i32 %low, ptr %variable, align 4
  call void @collect()
  %v = call i32 @leaf(ptr %variable)
  ret i32 %v
}

define i32 @integerCopyReturnsTwice(ptr %from, ptr %buf) {
  %gc.stackobject = alloca { i64, ptr, i32 }, align 8
  store { i64, ptr, i32 } zeroinitializer, ptr %gc.stackobject, align 8
  call void @tinygo_exposeStackObject(ptr %gc.stackobject)
  %both = load i64, ptr %from, align 8
  %1 = getelementptr { i64, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 0
  store i64 %both, ptr %1, align 8
  %again = call i32 @setjmp(ptr %buf)
  call void @collect()
  %low = trunc i64 %both to i32
  %p = inttoptr i32 %low to ptr
  %2 = getelementptr { i64, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 1
  store ptr %p, ptr %2, align 4
  %v = load i32, ptr %p, align 4
  %3 = getelementptr { i64, ptr, i32 }, ptr %gc.stackobject, i32 0, i32 2
  store i32 %v, ptr %3, align 4
  ret i32 %v
}

declare void @tinygo_exposeStackObject(ptr)

attributes #0 = { nocallback nofree nounwind willreturn memory(argmem: readwrite) }
attributes #1 = { returns_twice }

!0 = !{}

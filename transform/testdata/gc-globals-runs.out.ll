target datalayout = "e-m:e-p:32:32-p10:8:8-p20:8:8-i64:64-n32:64-S128-ni:1:10:20"
target triple = "wasm32-unknown-wasi"

@strings = global [6 x { ptr, i32 }] zeroinitializer
@slices = global [5 x { ptr, i32, i32 }] zeroinitializer
@structs = global [4 x { ptr, i32, ptr, ptr, i32 }] zeroinitializer
@sameSize = global [4 x { ptr, i32, i32, ptr, i32 }] zeroinitializer
@flat = global { ptr, ptr, i32, { ptr, i32 }, { ptr, i32 }, { ptr, i32 }, { ptr, i32 } } zeroinitializer
@two = global [2 x { ptr, i32 }] zeroinitializer
@single = global ptr null
@retyped = global i64 0
@noPointers = global [4 x i32] zeroinitializer
@runtime.gcGlobalRoots = internal constant [5 x { ptr, i32 }] [{ ptr, i32 } { ptr @flat, i32 8 }, { ptr, i32 } { ptr @two, i32 4 }, { ptr, i32 } { ptr getelementptr (i8, ptr @two, i32 8), i32 4 }, { ptr, i32 } { ptr @single, i32 4 }, { ptr, i32 } { ptr @retyped, i32 8 }]
@runtime.gcGlobalRootRuns = internal constant [7 x { ptr, i32, i32, i32 }] [{ ptr, i32, i32, i32 } { ptr @strings, i32 4, i32 8, i32 6 }, { ptr, i32, i32, i32 } { ptr @slices, i32 4, i32 12, i32 5 }, { ptr, i32, i32, i32 } { ptr @structs, i32 4, i32 20, i32 4 }, { ptr, i32, i32, i32 } { ptr getelementptr (i8, ptr @structs, i32 8), i32 8, i32 20, i32 4 }, { ptr, i32, i32, i32 } { ptr @sameSize, i32 4, i32 20, i32 4 }, { ptr, i32, i32, i32 } { ptr getelementptr (i8, ptr @sameSize, i32 12), i32 4, i32 20, i32 4 }, { ptr, i32, i32, i32 } { ptr getelementptr (i8, ptr @flat, i32 12), i32 4, i32 8, i32 4 }]

define i32 @runtime.gcGlobalRootCount() {
entry:
  ret i32 5
}

define ptr @runtime.gcGlobalRoot(i32 %0) {
entry:
  %1 = getelementptr inbounds [5 x { ptr, i32 }], ptr @runtime.gcGlobalRoots, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32 }, ptr %1, i32 0, i32 0
  %3 = load ptr, ptr %2, align 4
  ret ptr %3
}

define i32 @runtime.gcGlobalRootSize(i32 %0) {
entry:
  %1 = getelementptr inbounds [5 x { ptr, i32 }], ptr @runtime.gcGlobalRoots, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32 }, ptr %1, i32 0, i32 1
  %3 = load i32, ptr %2, align 4
  ret i32 %3
}

define i32 @runtime.gcGlobalRootRunCount() {
entry:
  ret i32 7
}

define ptr @runtime.gcGlobalRootRun(i32 %0) {
entry:
  %1 = getelementptr inbounds [7 x { ptr, i32, i32, i32 }], ptr @runtime.gcGlobalRootRuns, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32, i32, i32 }, ptr %1, i32 0, i32 0
  %3 = load ptr, ptr %2, align 4
  ret ptr %3
}

define i32 @runtime.gcGlobalRootRunSize(i32 %0) {
entry:
  %1 = getelementptr inbounds [7 x { ptr, i32, i32, i32 }], ptr @runtime.gcGlobalRootRuns, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32, i32, i32 }, ptr %1, i32 0, i32 1
  %3 = load i32, ptr %2, align 4
  ret i32 %3
}

define i32 @runtime.gcGlobalRootRunStride(i32 %0) {
entry:
  %1 = getelementptr inbounds [7 x { ptr, i32, i32, i32 }], ptr @runtime.gcGlobalRootRuns, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32, i32, i32 }, ptr %1, i32 0, i32 2
  %3 = load i32, ptr %2, align 4
  ret i32 %3
}

define i32 @runtime.gcGlobalRootRunLength(i32 %0) {
entry:
  %1 = getelementptr inbounds [7 x { ptr, i32, i32, i32 }], ptr @runtime.gcGlobalRootRuns, i32 0, i32 %0
  %2 = getelementptr inbounds nuw { ptr, i32, i32, i32 }, ptr %1, i32 0, i32 3
  %3 = load i32, ptr %2, align 4
  ret i32 %3
}

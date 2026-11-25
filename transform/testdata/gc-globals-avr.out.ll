target datalayout = "e-P1-p:16:8-i8:8-i16:8-i32:8-i64:8-f32:8-f64:8-n8:16-a:8"
target triple = "avr-unknown-unknown"

%entry = type { i8, ptr }

@table = global [4 x %entry] zeroinitializer
@pairs = global [5 x { ptr, i8, ptr }] zeroinitializer
@separate = global [5 x { ptr, i8, ptr, i8, i8 }] zeroinitializer
@small = global [2 x %entry] zeroinitializer
@single = global ptr null
@runtime.gcGlobalRoots = internal constant [5 x { ptr, i16 }] [{ ptr, i16 } { ptr @pairs, i16 2 }, { ptr, i16 } { ptr getelementptr (i8, ptr @pairs, i16 23), i16 2 }, { ptr, i16 } { ptr getelementptr (i8, ptr @small, i16 1), i16 2 }, { ptr, i16 } { ptr getelementptr (i8, ptr @small, i16 4), i16 2 }, { ptr, i16 } { ptr @single, i16 2 }]
@runtime.gcGlobalRootRuns = internal constant [4 x { ptr, i16, i16, i16 }] [{ ptr, i16, i16, i16 } { ptr getelementptr (i8, ptr @table, i16 1), i16 2, i16 3, i16 4 }, { ptr, i16, i16, i16 } { ptr getelementptr (i8, ptr @pairs, i16 3), i16 4, i16 5, i16 4 }, { ptr, i16, i16, i16 } { ptr @separate, i16 2, i16 7, i16 5 }, { ptr, i16, i16, i16 } { ptr getelementptr (i8, ptr @separate, i16 3), i16 2, i16 7, i16 5 }]

define i16 @runtime.gcGlobalRootCount() addrspace(1) {
entry:
  ret i16 5
}

define ptr @runtime.gcGlobalRoot(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [5 x { ptr, i16 }], ptr @runtime.gcGlobalRoots, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16 }, ptr %1, i32 0, i32 0
  %3 = load ptr, ptr %2, align 1
  ret ptr %3
}

define i16 @runtime.gcGlobalRootSize(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [5 x { ptr, i16 }], ptr @runtime.gcGlobalRoots, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16 }, ptr %1, i32 0, i32 1
  %3 = load i16, ptr %2, align 1
  ret i16 %3
}

define i16 @runtime.gcGlobalRootRunCount() addrspace(1) {
entry:
  ret i16 4
}

define ptr @runtime.gcGlobalRootRun(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [4 x { ptr, i16, i16, i16 }], ptr @runtime.gcGlobalRootRuns, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16, i16, i16 }, ptr %1, i32 0, i32 0
  %3 = load ptr, ptr %2, align 1
  ret ptr %3
}

define i16 @runtime.gcGlobalRootRunSize(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [4 x { ptr, i16, i16, i16 }], ptr @runtime.gcGlobalRootRuns, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16, i16, i16 }, ptr %1, i32 0, i32 1
  %3 = load i16, ptr %2, align 1
  ret i16 %3
}

define i16 @runtime.gcGlobalRootRunStride(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [4 x { ptr, i16, i16, i16 }], ptr @runtime.gcGlobalRootRuns, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16, i16, i16 }, ptr %1, i32 0, i32 2
  %3 = load i16, ptr %2, align 1
  ret i16 %3
}

define i16 @runtime.gcGlobalRootRunLength(i16 %0) addrspace(1) {
entry:
  %1 = getelementptr inbounds [4 x { ptr, i16, i16, i16 }], ptr @runtime.gcGlobalRootRuns, i32 0, i16 %0
  %2 = getelementptr inbounds nuw { ptr, i16, i16, i16 }, ptr %1, i32 0, i32 3
  %3 = load i16, ptr %2, align 1
  ret i16 %3
}

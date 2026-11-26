target datalayout = "e-m:e-p:32:32-p10:8:8-p20:8:8-i64:64-n32:64-S128-ni:1:10:20"
target triple = "wasm32-unknown-wasi"

; A table of strings: one run, 8 bytes apart.
@strings = global [6 x { ptr, i32 }] zeroinitializer

; A table of slices: one run, 12 bytes apart.
@slices = global [5 x { ptr, i32, i32 }] zeroinitializer

; Two separate pointer ranges of different sizes per element: two runs.
@structs = global [4 x { ptr, i32, ptr, ptr, i32 }] zeroinitializer

; Two separate pointer ranges of the same size per element, alternately 12 and
; 8 bytes apart: two runs, 20 bytes apart.
@sameSize = global [4 x { ptr, i32, i32, ptr, i32 }] zeroinitializer

; A table that interp gave a flat struct type, after a header: the header is a
; plain range, the strings a run.
@flat = global { ptr, ptr, i32, { ptr, i32 }, { ptr, i32 }, { ptr, i32 }, { ptr, i32 } } zeroinitializer

; Too short for a run.
@two = global [2 x { ptr, i32 }] zeroinitializer

@single = global ptr null

@retyped = global i64 0

@noPointers = global [4 x i32] zeroinitializer

declare i32 @runtime.gcGlobalRootCount()

declare ptr @runtime.gcGlobalRoot(i32)

declare i32 @runtime.gcGlobalRootSize(i32)

declare i32 @runtime.gcGlobalRootRunCount()

declare ptr @runtime.gcGlobalRootRun(i32)

declare i32 @runtime.gcGlobalRootRunSize(i32)

declare i32 @runtime.gcGlobalRootRunStride(i32)

declare i32 @runtime.gcGlobalRootRunLength(i32)

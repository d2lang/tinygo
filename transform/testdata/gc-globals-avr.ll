target datalayout = "e-P1-p:16:8-i8:8-i16:8-i32:8-i64:8-f32:8-f64:8-n8:16-a:8"
target triple = "avr-unknown-unknown"

%entry = type { i8, ptr }

; Pointers at odd offsets: one run of 4 ranges, 3 bytes apart.
@table = global [4 x %entry] zeroinitializer

; The second pointer of an element touches the first of the next one: a plain
; range at each end and a run of 4-byte ranges in between.
@pairs = global [5 x { ptr, i8, ptr }] zeroinitializer

; Two separate pointer ranges per element, alternately 3 and 4 bytes apart:
; two runs, 7 bytes apart.
@separate = global [5 x { ptr, i8, ptr, i8, i8 }] zeroinitializer

; Too short for a run.
@small = global [2 x %entry] zeroinitializer

@single = global ptr null

declare i16 @runtime.gcGlobalRootCount()

declare ptr @runtime.gcGlobalRoot(i16)

declare i16 @runtime.gcGlobalRootSize(i16)

declare i16 @runtime.gcGlobalRootRunCount()

declare ptr @runtime.gcGlobalRootRun(i16)

declare i16 @runtime.gcGlobalRootRunSize(i16)

declare i16 @runtime.gcGlobalRootRunStride(i16)

declare i16 @runtime.gcGlobalRootRunLength(i16)

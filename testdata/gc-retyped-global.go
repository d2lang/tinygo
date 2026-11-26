package main

// Globals that are only ever copied in and out of whole. InstCombine turns the
// copies into loads and stores of an integer, and GlobalOpt then gives each
// global that integer type ([2]*T becomes an i64), so their pointers are only
// found if globals of pointer-sized integers are scanned as well.
// Build with -tags=gc.stress to make a lost object show.

type T struct{ a, b int }

var g [2]*T
var g1 [1]*T
var gs [1]string
var sink []byte

//go:noinline
func fill(i int) {
	var l [2]*T
	l[0] = &T{a: i, b: 1}
	l[1] = &T{a: i + 100, b: 2}
	copy(g[:], l[:])
	var m [1]*T
	m[0] = &T{a: i + 1000, b: 3}
	copy(g1[:], m[:])
	var s [1]string
	b := make([]byte, 5)
	for j := range b {
		b[j] = byte('a' + i + j)
	}
	s[0] = string(b)
	copy(gs[:], s[:])
}

//go:noinline
func churn() {
	for i := 0; i < 8; i++ {
		sink = make([]byte, 16)
	}
}

//go:noinline
func read() int {
	var l [2]*T
	copy(l[:], g[:])
	var m [1]*T
	copy(m[:], g1[:])
	var s [1]string
	copy(s[:], gs[:])
	println(s[0])
	return l[0].a + l[1].a + m[0].a
}

func main() {
	for i := 0; i < 4; i++ {
		fill(i)
		churn()
		println(read())
	}
}

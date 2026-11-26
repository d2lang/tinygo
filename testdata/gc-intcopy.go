package main

// LLVM copies a small object as an integer of its size: copy() of one or two
// pointers, of a string or of an interface becomes a load and a store of an
// i32 or i64, and once the variable that was copied to is gone that integer is
// the only place the pointers are in. Every function here has them cross
// a collection that way, with the memory they were copied from cleared. The
// objects are lost if the compiler does not root the integer. Run with
// -tags=gc.stress to make sure a lost object is noticed.

type filler struct {
	a [8]int
	b int
}

var garbage []*filler

//go:noinline
func churn() {
	for i := 0; i < 6; i++ {
		x := new(filler)
		x.b = 0x55555555
		garbage = append(garbage, x)
	}
	garbage = nil
}

type pair struct{ arr [2]*int }

//go:noinline
func setPair(h *pair, a, b *int) { h.arr[0] = a; h.arr[1] = b }

//go:noinline
func clearPair(h *pair) { h.arr[0] = nil; h.arr[1] = nil; churn() }

// Two pointers in an i64, made pointers again after the call.
//
//go:noinline
func twoPointers(h *pair, v int) int {
	a := new(int)
	*a = v
	b := new(int)
	*b = v + 1
	setPair(h, a, b)
	var tmp [2]*int
	copy(tmp[:], h.arr[:])
	clearPair(h)
	return *tmp[0] + *tmp[1]
}

type strs struct{ s [1]string }

//go:noinline
func setString(h *strs, s string) { h.s[0] = s }

//go:noinline
func clearString(h *strs) { h.s[0] = "" }

// The pointer and the length of a string in an i64.
//
//go:noinline
func oneString(h *strs, v int) int {
	bs := make([]byte, 3)
	bs[0] = byte('a' + v)
	setString(h, string(bs))
	var tmp [1]string
	copy(tmp[:], h.s[:])
	clearString(h)
	churn()
	return int(tmp[0][0]) + len(tmp[0])
}

type ifaces struct{ s [1]interface{} }

//go:noinline
func setInterface(h *ifaces, s interface{}) { h.s[0] = s }

//go:noinline
func clearInterface(h *ifaces) { h.s[0] = nil }

// The type and the value of an interface in an i64.
//
//go:noinline
func oneInterface(h *ifaces, v int) int {
	a := new(int)
	*a = v
	setInterface(h, a)
	var tmp [1]interface{}
	copy(tmp[:], h.s[:])
	clearInterface(h)
	churn()
	return *tmp[0].(*int)
}

// The pointers are not made in this function: they come out of the memory of
// the caller's slice.
//
//go:noinline
func fromSlice(src []*int) int {
	var tmp [2]*int
	copy(tmp[:], src[:2])
	src[0], src[1] = nil, nil
	churn()
	return *tmp[0] + *tmp[1]
}

//go:noinline
func makeSlice(v int) []*int {
	s := make([]*int, 2)
	s[0] = new(int)
	*s[0] = v
	s[1] = new(int)
	*s[1] = v * 10
	return s
}

//go:noinline
func makeArray(v int) *[2]*int {
	a := new([2]*int)
	a[0] = new(int)
	*a[0] = v
	a[1] = new(int)
	*a[1] = v + 1
	return a
}

var source *[2]*int

// The integer is never made a pointer in this function: it is stored as it
// is after the collection.
//
//go:noinline
func storedLater(v int) int {
	dst := new([2]*int)
	source = makeArray(v)
	var tmp [2]*int
	copy(tmp[:], source[:])
	*source = [2]*int{}
	churn()
	copy(dst[:], tmp[:])
	churn()
	return *dst[0] + *dst[1]*3
}

// One pointer in an i32, carried round a loop.
//
//go:noinline
func oneInLoop(h *pair, n int) int {
	sum := 0
	var tmp [1]*int
	for i := 0; i < n; i++ {
		a := new(int)
		*a = i + 1
		setPair(h, a, nil)
		copy(tmp[:], h.arr[:1])
		clearPair(h)
		sum += *tmp[0]
	}
	return sum
}

func main() {
	for v := 1; v < 4; v++ {
		println(v, "twoPointers", twoPointers(new(pair), v), "want", 2*v+1)
		println(v, "oneString", oneString(new(strs), v), "want", 'a'+v+3)
		println(v, "oneInterface", oneInterface(new(ifaces), v), "want", v)
		println(v, "fromSlice", fromSlice(makeSlice(v)), "want", 11*v)
		println(v, "storedLater", storedLater(v), "want", 4*v+3)
		println(v, "oneInLoop", oneInLoop(new(pair), v+2), "want", (v+2)*(v+3)/2)
	}
}

package main

// Pointer identity: two different objects that are both still in use must not
// compare equal. LLVM copies a small object as an integer of its size, so the
// pointer that copy() takes out of memory can be an integer that is only
// compared after a collection. If the compiler does not root that integer, the
// object is freed, a new one is made at its address, and the two compare
// equal. With -tags=gc.stress that happens at the first allocation; without,
// after a few thousand.

type node struct {
	id  int
	pad [3]int
}

type pair struct{ a, b *node }

//go:noinline
func mkn(i int) *node { return &node{id: i} }

//go:noinline
func put(s []*node, n *node) { s[0] = n }

//go:noinline
func putp(s []pair, n *node) { s[0] = pair{n, n} }

//go:noinline
func puta(s []any, n *node) { s[0] = n }

// The old object is remembered through copy() and compared with every new
// one.
//
//go:noinline
func copiedPointer(s, t []*node, rounds int) int {
	n := mkn(1)
	put(s, n)
	var a, b [1]*node
	copy(a[:], s)
	s[0] = nil
	for i := 0; i < rounds; i++ {
		put(t, mkn(2))
		copy(b[:], t)
		if a[0] == b[0] {
			return i
		}
	}
	return -1
}

// The same with a struct of two pointers, which LLVM copies as an i64.
//
//go:noinline
func copiedPair(s, t []pair, rounds int) int {
	n := mkn(1)
	putp(s, n)
	var a, b [1]pair
	copy(a[:], s)
	s[0] = pair{}
	for i := 0; i < rounds; i++ {
		putp(t, mkn(2))
		copy(b[:], t)
		if a[0] == b[0] {
			return i
		}
	}
	return -1
}

// The same with interfaces that hold pointers.
//
//go:noinline
func copiedInterface(s, t []any, rounds int) int {
	n := mkn(1)
	puta(s, n)
	var a, b [1]any
	copy(a[:], s)
	s[0] = nil
	for i := 0; i < rounds; i++ {
		puta(t, mkn(2))
		copy(b[:], t)
		if a[0] == b[0] {
			return i
		}
	}
	return -1
}

// Remembered through copy(), compared with a pointer that is not copied.
//
//go:noinline
func copiedAgainstNew(s []*node, rounds int) int {
	n := mkn(1)
	put(s, n)
	var a [1]*node
	copy(a[:], s)
	s[0] = nil
	for i := 0; i < rounds; i++ {
		if a[0] == mkn(2) {
			return i
		}
	}
	return -1
}

// Remembered by a plain assignment: a pointer, which always kept its slot.
//
//go:noinline
func assigned(s, t []*node, rounds int) int {
	n := mkn(1)
	put(s, n)
	a := s[0]
	s[0] = nil
	for i := 0; i < rounds; i++ {
		put(t, mkn(2))
		if a == t[0] {
			return i
		}
	}
	return -1
}

func main() {
	const rounds = 200
	println("copiedPointer", copiedPointer(make([]*node, 1), make([]*node, 1), rounds))
	println("copiedPair", copiedPair(make([]pair, 1), make([]pair, 1), rounds))
	println("copiedInterface", copiedInterface(make([]any, 1), make([]any, 1), rounds))
	println("copiedAgainstNew", copiedAgainstNew(make([]*node, 1), rounds))
	println("assigned", assigned(make([]*node, 1), make([]*node, 1), rounds))
}

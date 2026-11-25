package main

// A value read from a map is copied out through a temporary buffer, and the
// map does not keep that copy alive. Here the map entry is replaced while the
// slice read from it is still being ranged over, and the loop allocates.
// Build with -tags=gc.stress to make a lost object show.

type T struct {
	v   int
	pad [6]int
}

var m = map[string][]*T{}

var sink []*T

//go:noinline
func churn() {
	for i := 0; i < 4; i++ {
		sink = append(sink, &T{v: -1})
	}
	sink = nil
}

// fill is a function of its own so that no stack slot of round holds the
// slice it puts into the map.
//
//go:noinline
func fill(r int) {
	s := make([]*T, 6)
	for i := range s {
		s[i] = &T{v: r*10 + i}
	}
	m["k"] = s
}

//go:noinline
func round(r int) int {
	fill(r)
	sum := 0
	for _, t := range m["k"] {
		m["k"] = make([]*T, 6)
		// Another lookup of the same type may reuse the temporary buffer
		// that the first one copied the slice header out of.
		if x := m["other"]; len(x) != 0 {
			sum += 1000
		}
		churn()
		sum += t.v
	}
	return sum
}

func main() {
	for r := 0; r < 8; r++ {
		println("round", r, "sum", round(r))
	}
}

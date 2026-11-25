package transform

import (
	"math/rand"
	"testing"
)

// TestGCGlobalRootRunsRandom checks makeRuns and check on many layouts:
// tables of elements with one or more separate pointer ranges, after a header,
// for both the WebAssembly and the AVR entry sizes.
func TestGCGlobalRootRunsRandom(t *testing.T) {
	t.Parallel()
	encodings := []gcGlobalRootEncoding{
		{pointerSize: 4, rootBytes: 8, runBytes: 16},
		{pointerSize: 2, rootBytes: 4, runBytes: 8},
	}
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20000; iter++ {
		e := encodings[iter%len(encodings)]
		var ranges []gcGlobalRootRange
		offset := uint64(0)
		add := func(words uint64, gap uint64) {
			ranges = append(ranges, gcGlobalRootRange{offset: offset, size: words * e.pointerSize})
			offset += words*e.pointerSize + gap
		}
		// A header of plain ranges.
		for n := rng.Intn(3); n > 0; n-- {
			add(uint64(1+rng.Intn(3)), uint64(1+rng.Intn(9)))
		}
		// A table: each element has 1-4 ranges.
		parts := 1 + rng.Intn(4)
		words := make([]uint64, parts)
		gaps := make([]uint64, parts)
		for i := range words {
			words[i] = uint64(1 + rng.Intn(3))
			gaps[i] = uint64(1 + rng.Intn(9))
		}
		for n := rng.Intn(12); n > 0; n-- {
			for i := range words {
				add(words[i], gaps[i])
			}
		}
		// Sometimes a trailer.
		if rng.Intn(2) == 0 {
			add(uint64(1+rng.Intn(3)), 1)
		}
		roots, runs := e.makeRuns(ranges)
		if err := e.check(ranges, roots, runs); err != nil {
			t.Fatalf("ranges %v: %v", ranges, err)
		}
		if bytes := uint64(len(roots))*e.rootBytes + uint64(len(runs))*e.runBytes; bytes > uint64(len(ranges))*e.rootBytes {
			t.Fatalf("ranges %v: %d bytes, more than the %d of the plain table", ranges, bytes, uint64(len(ranges))*e.rootBytes)
		}
		// The check must notice a run that scans one range too many or too
		// few, and a missing plain range.
		if len(runs) != 0 {
			runs[0].count++
			if e.check(ranges, roots, runs) == nil {
				t.Fatalf("ranges %v: check missed an extra range", ranges)
			}
			runs[0].count -= 2
			if e.check(ranges, roots, runs) == nil {
				t.Fatalf("ranges %v: check missed a missing range", ranges)
			}
			runs[0].count++
		}
		if len(roots) != 0 && e.check(ranges, roots[1:], runs) == nil {
			t.Fatalf("ranges %v: check missed a missing plain range", ranges)
		}
	}
}

// TestGCGlobalRootRunsCheckWords checks that check compares words, not only
// ranges: ranges that differ only in how words are split up are different
// calls to the custom and Boehm collectors' range scanning function.
func TestGCGlobalRootRunsCheckWords(t *testing.T) {
	e := gcGlobalRootEncoding{pointerSize: 4, rootBytes: 8, runBytes: 16}
	want := []gcGlobalRootRange{{offset: 0, size: 8}}
	if e.check(want, []gcGlobalRootRange{{offset: 0, size: 4}, {offset: 4, size: 4}}, nil) == nil {
		t.Error("check took two ranges for one")
	}
	if e.check(want, nil, []gcGlobalRootRun{{first: gcGlobalRootRange{offset: 0, size: 4}, stride: 4, count: 2}}) == nil {
		t.Error("check took a run of two ranges for one range")
	}
	if err := e.check(want, want, nil); err != nil {
		t.Error(err)
	}
}

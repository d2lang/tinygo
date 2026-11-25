package main

// Every function here keeps an object alive through one particular kind of
// local value while a collection runs, and then reads the object. The object
// is reachable from nowhere else, so it is lost if the compiler does not root
// that value. Run with -tags=gc.stress to make sure a lost object is noticed.

import (
	"runtime"
	"strconv"
	"unsafe"
)

type node struct {
	v    int
	arr  [8]int
	next *node
}

func (n *node) value() int { return n.v }

type valuer interface{ value() int }

type box struct {
	p   *node
	pad [3]int
}

var garbage []*node

// churn collects and then allocates over whatever was freed.
//
//go:noinline
func churn() {
	runtime.GC()
	for i := 0; i < 32; i++ {
		n := &node{v: -1}
		for j := range n.arr {
			n.arr[j] = -1
		}
		garbage = append(garbage, n)
	}
	garbage = nil
}

//go:noinline
func newNode(v int) *node {
	n := &node{v: v}
	for i := range n.arr {
		n.arr[i] = v + i
	}
	return n
}

//go:noinline
func check(name string, got, want int) {
	if got != want {
		println(name, "FAILED: got", got, "want", want)
		return
	}
	println(name, "ok")
}

//go:noinline
func acrossCall() int {
	p := newNode(1)
	churn()
	return p.v
}

//go:noinline
func readAfterChurn(p *node) int {
	churn()
	return p.arr[2]
}

//go:noinline
func asArgument() int {
	return readAfterChurn(newNode(10))
}

//go:noinline
func interiorPointer() int {
	p := newNode(20)
	q := &p.arr[3]
	p = nil
	churn()
	return *q
}

//go:noinline
func sliceToTheEnd() int {
	s := make([]int, 40)
	for i := range s {
		s[i] = i
	}
	sum := 0
	for len(s) > 0 {
		churn()
		sum += s[0]
		s = s[1:]
	}
	return sum
}

//go:noinline
func loopCarried() int {
	prev := newNode(0)
	sum := 0
	for i := 1; i <= 5; i++ {
		cur := newNode(i)
		churn()
		sum += prev.v*10 + cur.arr[1]
		prev = cur
	}
	return sum + prev.v
}

//go:noinline
func mapLookup() int {
	m := map[string]*node{}
	m["a"] = newNode(30)
	m["b"] = newNode(31)
	v := m["a"]
	delete(m, "a")
	churn()
	return v.v + m["b"].v
}

//go:noinline
func interfaceValue() int {
	var i valuer = newNode(40)
	churn()
	return i.value()
}

//go:noinline
func closure() int {
	p := newNode(50)
	f := func() int { return p.arr[4] }
	churn()
	return f()
}

//go:noinline
func boundMethod() int {
	p := newNode(60)
	f := p.value
	p = nil
	churn()
	return f()
}

//go:noinline
func deferred() (result int) {
	p := newNode(70)
	defer func(n *node) {
		churn()
		result += n.v
	}(p)
	p = newNode(1)
	churn()
	return p.v
}

//go:noinline
func deferredInLoop() (result int) {
	for i := 0; i < 4; i++ {
		defer func(n *node) {
			churn()
			result += n.v
		}(newNode(i))
		churn()
	}
	return 100
}

//go:noinline
func mayPanic(n *node) {
	churn()
	if n.v > 0 {
		panic(n)
	}
}

//go:noinline
func recovered() (result int) {
	keep := newNode(80)
	defer func() {
		r := recover()
		churn()
		result = r.(*node).v + keep.arr[1]
	}()
	mayPanic(newNode(5))
	return -1
}

//go:noinline
func unsafeArithmetic() int {
	p := newNode(90)
	q := (*int)(unsafe.Add(unsafe.Pointer(p), unsafe.Offsetof(p.arr)+2*unsafe.Sizeof(p.arr[0])))
	p = nil
	churn()
	return *q
}

//go:noinline
func stringsAndBytes(i int) int {
	s := strconv.Itoa(i) + "-" + strconv.Itoa(i+1)
	b := []byte(s)
	churn()
	t := string(b[1:]) + s
	churn()
	return len(t)*100 + int(t[0]-'0')
}

//go:noinline
func appended() int {
	var s []*node
	for i := 0; i < 20; i++ {
		s = append(s, newNode(i))
		churn()
	}
	sum := 0
	for _, n := range s {
		sum += n.arr[1]
	}
	return sum
}

//go:noinline
func two(v int) (*node, *node) {
	return newNode(v), newNode(v + 1)
}

//go:noinline
func twoResults() int {
	a, b := two(110)
	churn()
	return a.v + b.v
}

//go:noinline
func channel() int {
	ch := make(chan *node, 2)
	ch <- newNode(120)
	churn()
	n := <-ch
	churn()
	return n.v
}

//go:noinline
func typeSwitch(x interface{}) int {
	churn()
	switch x := x.(type) {
	case *node:
		churn()
		return x.v
	case string:
		churn()
		return len(x)
	}
	return -1
}

//go:noinline
func stackArray(i int) int {
	var a [4]*node
	a[i&3] = newNode(130)
	a[(i+1)&3] = newNode(131)
	churn()
	return a[i&3].v + a[(i+1)&3].v
}

//go:noinline
func fill(b *box, v int) {
	b.p = newNode(v)
}

//go:noinline
func filledByCallee() int {
	var b box
	fill(&b, 140)
	churn()
	return b.p.v
}

//go:noinline
func structValue(b box) box {
	churn()
	b.pad[0] = b.p.v
	return b
}

//go:noinline
func byValue() int {
	b := structValue(box{p: newNode(150)})
	churn()
	return b.p.v + b.pad[0]
}

//go:noinline
func linkedList() int {
	var head *node
	for i := 0; i < 10; i++ {
		head = &node{v: i, next: head}
		churn()
	}
	sum := 0
	for n := head; n != nil; n = n.next {
		churn()
		sum += n.v
	}
	return sum
}

//go:noinline
func selected(i int) int {
	a, b := newNode(160), newNode(161)
	p := a
	if i&1 != 0 {
		p = b
	}
	a, b = nil, nil
	churn()
	return p.v
}

// rangeOverLookup ranges over a slice that it read from a map while the loop
// body replaces that map entry and looks up another one. From then on only the
// loop itself knows the slice.
//
//go:noinline
func rangeOverLookup() int {
	m := map[int][]*node{}
	for i := 0; i < 4; i++ {
		m[i] = []*node{newNode(170 + i), newNode(180 + i), newNode(190 + i)}
	}
	sum := 0
	for _, n := range m[1] {
		m[1] = nil
		churn()
		for _, o := range m[2] {
			sum += o.arr[1] - o.v - 1
		}
		sum += n.v
	}
	return sum
}

func main() {
	check("across a call", acrossCall(), 1)
	check("as an argument", asArgument(), 12)
	check("interior pointer", interiorPointer(), 23)
	check("slice to the end", sliceToTheEnd(), 780)
	check("carried round a loop", loopCarried(), 125)
	check("map lookup", mapLookup(), 61)
	check("interface value", interfaceValue(), 40)
	check("closure", closure(), 54)
	check("bound method", boundMethod(), 60)
	check("deferred call", deferred(), 71)
	check("deferred calls in a loop", deferredInLoop(), 106)
	check("recovered panic", recovered(), 86)
	check("unsafe arithmetic", unsafeArithmetic(), 92)
	check("strings and bytes", stringsAndBytes(41), 901)
	check("appended", appended(), 210)
	check("two results", twoResults(), 221)
	check("channel", channel(), 120)
	check("type switch", typeSwitch(newNode(125))+typeSwitch("abc"), 128)
	check("array on the stack", stackArray(1), 261)
	check("filled by a callee", filledByCallee(), 140)
	check("struct by value", byValue(), 300)
	check("linked list", linkedList(), 45)
	check("selected", selected(0)+selected(1), 321)
	check("range over a map lookup", rangeOverLookup(), 543)
}

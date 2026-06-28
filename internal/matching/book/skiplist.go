package book

// 此处跳表的mutex锁为防御性冗余设计，实际SkipList在actor单线程的Orderbook里跑，不需要加锁，可删除
import (
	"math/rand"
	"sync"
)

const (
	MaxLevel = 32
	P        = 0.36787944117 // 1/e
)

// SkipListNode is a node in the skip list.
type SkipListNode[K any] struct {
	Key      K
	Forward  []*SkipListNode[K]
	Backward *SkipListNode[K]
}

// LessFunc is a function that compares two keys.
type LessFunc[K any] func(a, b K) bool

// SkipList is a generic skip list data structure.
type SkipList[K any] struct {
	less     LessFunc[K]
	header   *SkipListNode[K]
	level    int
	length   int
	mu       sync.Mutex
	maxLevel int
	prob     float64
}

// NewSkipList creates a new skip list with the given less function.
func NewSkipList[K any](less LessFunc[K]) *SkipList[K] {
	header := &SkipListNode[K]{
		Key:     *new(K),
		Forward: make([]*SkipListNode[K], MaxLevel),
	}
	return &SkipList[K]{
		less:     less,
		header:   header,
		level:    0,
		length:   0,
		maxLevel: MaxLevel,
		prob:     P,
	}
}

// randomLevel generates a random level for a new node.
// Level k is chosen with probability prob^(k-1) * (1-prob).
func (sl *SkipList[K]) randomLevel() int {
	level := 1
	for level < sl.maxLevel && rand.Float64() < sl.prob {
		level++
	}
	return level
}

// Insert inserts a key into the skip list.
// If the key already exists, returns the existing node without inserting.
func (sl *SkipList[K]) Insert(key K) *SkipListNode[K] {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := make([]*SkipListNode[K], sl.maxLevel)
	curr := sl.header

	for i := sl.level - 1; i >= 0; i-- {
		for curr.Forward[i] != nil && sl.less(curr.Forward[i].Key, key) {
			curr = curr.Forward[i]
		}
		update[i] = curr
	}

	curr = curr.Forward[0]
	if curr != nil && !sl.less(key, curr.Key) && !sl.less(curr.Key, key) {
		return curr
	}

	rl := sl.randomLevel()
	if rl > sl.level {
		for i := sl.level; i < rl; i++ {
			update[i] = sl.header
		}
		sl.level = rl
	}

	node := &SkipListNode[K]{
		Key:     key,
		Forward: make([]*SkipListNode[K], rl),
	}

	for i := 0; i < rl; i++ {
		node.Forward[i] = update[i].Forward[i]
		update[i].Forward[i] = node
	}

	if rl > 0 {
		node.Backward = update[0]
		if node.Forward[0] != nil {
			node.Forward[0].Backward = node
		}
	}

	sl.length++
	return node
}

// Search searches for a key in the skip list.
// Returns the node with the exact key, or nil if not found.
func (sl *SkipList[K]) Search(key K) *SkipListNode[K] {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	curr := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for curr.Forward[i] != nil && sl.less(curr.Forward[i].Key, key) {
			curr = curr.Forward[i]
		}
	}
	curr = curr.Forward[0]
	if curr != nil && !sl.less(key, curr.Key) && !sl.less(curr.Key, key) {
		return curr
	}
	return nil
}

// Seek returns the first node with key >= given key (lower bound).
// Returns nil only if the skip list is empty.
func (sl *SkipList[K]) Seek(key K) *SkipListNode[K] {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	curr := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for curr.Forward[i] != nil && sl.less(curr.Forward[i].Key, key) {
			curr = curr.Forward[i]
		}
	}
	// curr is the rightmost node with key < given key
	if curr.Forward[0] != nil {
		return curr.Forward[0]
	}
	// No node with key >= given key found; the seek is past all keys.
	// Find the actual last node by walking the level-0 chain.
	if curr != sl.header {
		return curr
	}
	return nil
}

// Delete removes a key from the skip list.
// Returns true if a node was removed, false otherwise.
func (sl *SkipList[K]) Delete(key K) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := make([]*SkipListNode[K], sl.maxLevel)
	curr := sl.header

	for i := sl.level - 1; i >= 0; i-- {
		for curr.Forward[i] != nil && sl.less(curr.Forward[i].Key, key) {
			curr = curr.Forward[i]
		}
		update[i] = curr
	}

	curr = curr.Forward[0]
	if curr == nil || (sl.less(key, curr.Key) || sl.less(curr.Key, key)) {
		return false
	}

	if curr.Forward[0] != nil {
		curr.Forward[0].Backward = curr.Backward
	}
	if curr.Backward != nil {
		curr.Backward.Forward[0] = curr.Forward[0]
	}

	for i := 0; i < sl.level; i++ {
		if update[i].Forward[i] == curr {
			update[i].Forward[i] = curr.Forward[i]
		}
	}

	for sl.level > 0 && sl.header.Forward[sl.level-1] == nil {
		sl.level--
	}

	sl.length--
	return true
}

// Len returns the number of elements in the skip list.
func (sl *SkipList[K]) Len() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.length
}

// IsEmpty returns true if the skip list is empty.
func (sl *SkipList[K]) IsEmpty() bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.length == 0
}

// SeekFirst returns the first node in the skip list (lowest key).
func (sl *SkipList[K]) SeekFirst() *SkipListNode[K] {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.header.Forward[0]
}

// SeekLast returns the last node in the skip list (highest key).
func (sl *SkipList[K]) SeekLast() *SkipListNode[K] {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	curr := sl.header
	for i := sl.level - 1; i >= 0; i-- {
		for curr.Forward[i] != nil {
			curr = curr.Forward[i]
		}
	}
	if curr == sl.header {
		return nil
	}
	return curr
}

// Next returns the next node in the skip list.
func (n *SkipListNode[K]) Next() *SkipListNode[K] {
	if len(n.Forward) == 0 {
		return nil
	}
	return n.Forward[0]
}

// Prev returns the previous node in the skip list.
func (n *SkipListNode[K]) Prev() *SkipListNode[K] {
	return n.Backward
}

// SkipListIterator iterates over a skip list.
type SkipListIterator[K any] struct {
	sl   *SkipList[K]
	node *SkipListNode[K]
}

// Iter returns an iterator starting from the given node.
func (sl *SkipList[K]) Iter(node *SkipListNode[K]) *SkipListIterator[K] {
	return &SkipListIterator[K]{sl: sl, node: node}
}

// Next returns the next node in the iteration.
func (it *SkipListIterator[K]) Next() *SkipListNode[K] {
	if it.node == nil {
		return nil
	}
	it.node = it.node.Next()
	return it.node
}

package book_test

import (
	"math"
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/stretchr/testify/assert"
)

func intLess(a, b int) bool { return a < b }
func float64Less(a, b float64) bool { return a < b }

func TestSkipList_InsertAndSearch(t *testing.T) {
	sl := book.NewSkipList(intLess)

	node := sl.Insert(10)
	assert.NotNil(t, node)
	assert.Equal(t, 10, node.Key)

	found := sl.Search(10)
	assert.NotNil(t, found)
	assert.Equal(t, 10, found.Key)

	notFound := sl.Search(5)
	assert.Nil(t, notFound)
}

func TestSkipList_InsertDuplicate(t *testing.T) {
	sl := book.NewSkipList(intLess)

	n1 := sl.Insert(10)
	n2 := sl.Insert(10)

	assert.Equal(t, n1, n2)
	assert.Equal(t, 1, sl.Len())
}

func TestSkipList_Delete(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)

	assert.Equal(t, 3, sl.Len())

	deleted := sl.Delete(20)
	assert.True(t, deleted)
	assert.Equal(t, 2, sl.Len())

	found := sl.Search(20)
	assert.Nil(t, found)

	found10 := sl.Search(10)
	assert.NotNil(t, found10)

	found30 := sl.Search(30)
	assert.NotNil(t, found30)
}

func TestSkipList_DeleteNonExistent(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	deleted := sl.Delete(999)
	assert.False(t, deleted)
	assert.Equal(t, 1, sl.Len())
}

func TestSkipList_Seek(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)
	sl.Insert(40)
	sl.Insert(50)

	// Seek exact
	node := sl.Seek(30)
	assert.NotNil(t, node)
	assert.Equal(t, 30, node.Key)

	// Seek lower bound
	node = sl.Seek(25)
	assert.NotNil(t, node)
	assert.Equal(t, 30, node.Key)

	// Seek above all → returns last node
	node = sl.Seek(100)
	assert.NotNil(t, node)
	assert.Equal(t, 50, node.Key)

	// Seek below all
	node = sl.Seek(5)
	assert.NotNil(t, node)
	assert.Equal(t, 10, node.Key)
}

func TestSkipList_SeekFirst(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(5)

	node := sl.SeekFirst()
	assert.NotNil(t, node)
	assert.Equal(t, 5, node.Key)
}

func TestSkipList_SeekLast(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)

	node := sl.SeekLast()
	assert.NotNil(t, node)
	assert.Equal(t, 30, node.Key)
}

func TestSkipList_SeekFirstEmpty(t *testing.T) {
	sl := book.NewSkipList(intLess)

	node := sl.SeekFirst()
	assert.Nil(t, node)
}

func TestSkipList_SeekLastEmpty(t *testing.T) {
	sl := book.NewSkipList(intLess)

	node := sl.SeekLast()
	assert.Nil(t, node)
}

func TestSkipList_SeekInf(t *testing.T) {
	sl := book.NewSkipList(float64Less)

	sl.Insert(100.0)
	sl.Insert(200.0)
	sl.Insert(50.0)

	// Seek +Inf returns last node
	last := sl.Seek(math.Inf(1))
	assert.NotNil(t, last)
	assert.Equal(t, 200.0, last.Key)

	// Seek -Inf returns first node
	first := sl.Seek(math.Inf(-1))
	assert.NotNil(t, first)
	assert.Equal(t, 50.0, first.Key)
}

func TestSkipList_Iterator(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)

	var keys []int
	for node := sl.SeekFirst(); node != nil; node = node.Next() {
		keys = append(keys, node.Key)
	}

	assert.Equal(t, []int{10, 20, 30}, keys)
}

func TestSkipList_IteratorFromMiddle(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)

	node := sl.Seek(20)
	assert.NotNil(t, node)

	var keys []int
	keys = append(keys, node.Key)
	for node = node.Next(); node != nil; node = node.Next() {
		keys = append(keys, node.Key)
	}

	assert.Equal(t, []int{20, 30}, keys)
}

func TestSkipList_Len(t *testing.T) {
	sl := book.NewSkipList(intLess)

	assert.Equal(t, 0, sl.Len())

	sl.Insert(10)
	assert.Equal(t, 1, sl.Len())

	sl.Insert(20)
	assert.Equal(t, 2, sl.Len())

	sl.Delete(10)
	assert.Equal(t, 1, sl.Len())
}

func TestSkipList_IsEmpty(t *testing.T) {
	sl := book.NewSkipList(intLess)

	assert.True(t, sl.IsEmpty())

	sl.Insert(10)
	assert.False(t, sl.IsEmpty())

	sl.Delete(10)
	assert.True(t, sl.IsEmpty())
}

func TestSkipList_DeleteMaintainsStructure(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)
	sl.Insert(40)
	sl.Insert(50)

	sl.Delete(30)

	var keys []int
	for node := sl.SeekFirst(); node != nil; node = node.Next() {
		keys = append(keys, node.Key)
	}

	assert.Equal(t, []int{10, 20, 40, 50}, keys)

	assert.Nil(t, sl.Search(30))
	assert.NotNil(t, sl.Search(10))
	assert.NotNil(t, sl.Search(20))
	assert.NotNil(t, sl.Search(40))
	assert.NotNil(t, sl.Search(50))
}

func TestSkipList_IterMethod(t *testing.T) {
	sl := book.NewSkipList(intLess)

	sl.Insert(10)
	sl.Insert(20)
	sl.Insert(30)

	var keys []int
	for node := sl.SeekFirst(); node != nil; node = node.Next() {
		keys = append(keys, node.Key)
	}

	assert.Equal(t, []int{10, 20, 30}, keys)
}

func BenchmarkSkipListInsert_10KLevels(b *testing.B) {
	b.StopTimer()
	prices := make([]float64, 10000)
	for i := 0; i < 10000; i++ {
		prices[i] = float64(i)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		sl := book.NewSkipList(float64Less)
		for _, p := range prices {
			sl.Insert(p)
		}
	}
}

func BenchmarkSkipListInsert_Duplicate(b *testing.B) {
	b.StopTimer()
	sl := book.NewSkipList(intLess)
	for i := 0; i < 100; i++ {
		sl.Insert(i)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		sl.Insert(50)
	}
}

func BenchmarkSkipListSearch(b *testing.B) {
	b.StopTimer()
	sl := book.NewSkipList(intLess)
	for i := 0; i < 10000; i++ {
		sl.Insert(i)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		sl.Search(i % 10000)
	}
}

func BenchmarkSkipListSeek(b *testing.B) {
	b.StopTimer()
	sl := book.NewSkipList(intLess)
	for i := 0; i < 10000; i++ {
		sl.Insert(i)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		sl.Seek(i % 10000)
	}
}

func BenchmarkSkipListDelete(b *testing.B) {
	b.StopTimer()
	sl := book.NewSkipList(intLess)
	for i := 0; i < 10000; i++ {
		sl.Insert(i)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		sl.Delete(i % 10000)
	}
}

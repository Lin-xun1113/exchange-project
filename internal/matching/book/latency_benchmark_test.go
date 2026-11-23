package book_test

import (
	"strconv"
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/book"
)

func BenchmarkBook_10KPriceLevels(b *testing.B) {
	for _, name := range []string{"resting", "matching"} {
		b.Run(name, func(b *testing.B) {
			ob := book.New()
			for i := range 10_000 {
				ob.Submit(order(strconv.Itoa(i), 1, book.Sell, book.Limit, int64(i+1), 1_000_000_000))
			}
			taker := order("taker", 2, book.Buy, book.IOC, 1, 1)
			if name == "resting" {
				taker = order("ask", 2, book.Sell, book.Limit, 10_001, 1)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := ob.CheckSubmit(taker); err != nil {
					b.Fatal(err)
				}
				if ob.Submit(taker).Resting {
					ob.Cancel(taker.ID)
				}
			}
		})
	}
}

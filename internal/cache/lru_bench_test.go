package cache

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkLRU_Put(b *testing.B) {
	lru := NewLRU[string, bool](10000, 5*time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lru.Put(fmt.Sprintf("key-%d", i), true)
	}
}

func BenchmarkLRU_Get_Hit(b *testing.B) {
	lru := NewLRU[string, bool](10000, 5*time.Minute)
	for i := 0; i < 10000; i++ {
		lru.Put(fmt.Sprintf("key-%d", i), true)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lru.Get(fmt.Sprintf("key-%d", i%10000))
	}
}

func BenchmarkLRU_Get_Miss(b *testing.B) {
	lru := NewLRU[string, bool](10000, 5*time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lru.Get(fmt.Sprintf("miss-%d", i))
	}
}

func BenchmarkLRU_Put_Eviction(b *testing.B) {
	lru := NewLRU[string, bool](100, 5*time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lru.Put(fmt.Sprintf("key-%d", i), true)
	}
}

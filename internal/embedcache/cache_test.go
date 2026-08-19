package embedcache

import "testing"

func TestFingerprintStableAndBounded(t *testing.T) {
	a := Fingerprint([]byte(`{"model":"m1","input":"hello world"}`))
	b := Fingerprint([]byte(`{"model":"m1","input":"hello world"}`))
	if a != b {
		t.Fatalf("same input produced different fingerprints: %s vs %s", a, b)
	}
	if Fingerprint([]byte(`{"model":"m2","input":"hello world"}`)) == a {
		t.Fatal("different model produced same fingerprint")
	}
	if Fingerprint([]byte(`{"model":"m1","input":"hello world","dimensions":256}`)) == a {
		t.Fatal("response-affecting request field was omitted from fingerprint")
	}
	if len(a) > 80 {
		t.Fatalf("fingerprint unbounded length %d", len(a))
	}
}

func TestCacheGetPutRoundTrip(t *testing.T) {
	cache := New(4)
	key := Fingerprint([]byte(`{"model":"m","input":"a"}`))
	if _, ok := cache.Get(key); ok {
		t.Fatal("fresh cache returned a hit")
	}
	cache.Put(key, []byte(`{"data":[]}`))
	got, ok := cache.Get(key)
	if !ok || string(got) != `{"data":[]}` {
		t.Fatalf("Get after Put = %q, %v", got, ok)
	}
}

func TestCacheEvictsLRU(t *testing.T) {
	cache := New(3)
	keys := []string{"a", "b", "c", "d"}
	for index, key := range keys {
		cache.Put(key, []byte(key))
		if cache.Len() > 3 {
			t.Fatalf("cache grew beyond bound after %d puts", index+1)
		}
	}
	if cache.Len() != 3 {
		t.Fatalf("cache len = %d, want 3", cache.Len())
	}
	// "a" was evicted first.
	if _, ok := cache.Get("a"); ok {
		t.Fatal("evicted key still present")
	}
	if _, ok := cache.Get("d"); !ok {
		t.Fatal("most recent key missing")
	}
}

func TestCacheRefreshMovesToFront(t *testing.T) {
	cache := New(3)
	cache.Put("a", []byte("a"))
	cache.Put("b", []byte("b"))
	cache.Put("c", []byte("c"))
	// Touch "a" so it is now most recent; then "d" evicts "b" (LRU).
	_, _ = cache.Get("a")
	cache.Put("d", []byte("d"))
	if _, ok := cache.Get("a"); !ok {
		t.Fatal("refreshed key was evicted")
	}
	if _, ok := cache.Get("b"); ok {
		t.Fatal("LRU key not evicted after refresh")
	}
}

func TestCacheDuplicatePutRefreshes(t *testing.T) {
	cache := New(2)
	cache.Put("a", []byte("a"))
	cache.Put("a", []byte("a-updated"))
	got, ok := cache.Get("a")
	if !ok || string(got) != "a-updated" {
		t.Fatalf("duplicate Put did not refresh value: %q, %v", got, ok)
	}
}

func TestCacheBoundsTotalResponseBytes(t *testing.T) {
	cache := New(10)
	response := make([]byte, (32<<20)+1)
	cache.Put("a", response)
	cache.Put("b", response)

	if cache.Len() != 1 {
		t.Fatalf("cache len = %d after exceeding byte bound, want 1", cache.Len())
	}
	if _, ok := cache.Get("a"); ok {
		t.Fatal("least-recently-used response survived the byte-bound eviction")
	}
	if _, ok := cache.Get("b"); !ok {
		t.Fatal("most-recently-used response was evicted")
	}
}

func BenchmarkCacheGetPut(b *testing.B) {
	cache := New(256)
	key := Fingerprint([]byte(`{"model":"nvidia/nv-embed-v1","input":"benchmark sample text"}`))
	payload := []byte(`{"object":"list","data":[{"embedding":[0.1,0.2,0.3],"index":0}]}`)
	cache.Put(key, payload)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get(key)
	}
}

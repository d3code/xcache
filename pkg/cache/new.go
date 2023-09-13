package cache

import (
    "runtime"
    "time"
)

func newCache(de time.Duration, m map[string]Item) *cache {
    if de == 0 {
        de = -1
    }
    c := &cache{
        defaultExpiration: de,
        items:             m,
    }
    return c
}

func newCacheWithJanitor(de time.Duration, ci time.Duration, m map[string]Item) *Cache {
    c := newCache(de, m)
    C := &Cache{c}
    if ci > 0 {
        runJanitor(c, ci)
        runtime.SetFinalizer(C, stopJanitor)
    }
    return C
}

func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
    items := make(map[string]Item)
    return newCacheWithJanitor(defaultExpiration, cleanupInterval, items)
}

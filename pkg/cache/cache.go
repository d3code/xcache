package cache

import (
    "encoding/gob"
    "fmt"
    "io"
    "os"
    "sync"
    "time"
)

type Item struct {
    Object     interface{}
    Expiration int64
}

func (item Item) Expired() bool {
    if item.Expiration == 0 {
        return false
    }
    return time.Now().UnixNano() > item.Expiration
}

const (
    NoExpiration      time.Duration = -1
    DefaultExpiration time.Duration = 0
)

type Cache struct {
    *cache
}

type cache struct {
    defaultExpiration time.Duration
    items             map[string]Item
    mu                sync.RWMutex
    onEvicted         func(string, interface{})
    janitor           *janitor
}

func (c *cache) Set(k string, x interface{}, d time.Duration) {
    var e int64
    if d == DefaultExpiration {
        d = c.defaultExpiration
    }
    if d > 0 {
        e = time.Now().Add(d).UnixNano()
    }
    c.mu.Lock()
    c.items[k] = Item{
        Object:     x,
        Expiration: e,
    }
    c.mu.Unlock()
}

func (c *cache) set(k string, x interface{}, d time.Duration) {
    var e int64
    if d == DefaultExpiration {
        d = c.defaultExpiration
    }
    if d > 0 {
        e = time.Now().Add(d).UnixNano()
    }
    c.items[k] = Item{
        Object:     x,
        Expiration: e,
    }
}

func (c *cache) SetDefault(k string, x interface{}) {
    c.Set(k, x, DefaultExpiration)
}

func (c *cache) Add(k string, x interface{}, d time.Duration) error {
    c.mu.Lock()
    _, found := c.get(k)
    if found {
        c.mu.Unlock()
        return fmt.Errorf("item %s already exists", k)
    }
    c.set(k, x, d)
    c.mu.Unlock()
    return nil
}

func (c *cache) Replace(k string, x interface{}, d time.Duration) error {
    c.mu.Lock()
    _, found := c.get(k)
    if !found {
        c.mu.Unlock()
        return fmt.Errorf("item %s doesn't exist", k)
    }
    c.set(k, x, d)
    c.mu.Unlock()
    return nil
}

func (c *cache) Get(k string) (interface{}, bool) {
    c.mu.RLock()
    // "Inlining" of get and Expired
    item, found := c.items[k]
    if !found {
        c.mu.RUnlock()
        return nil, false
    }
    if item.Expiration > 0 {
        if time.Now().UnixNano() > item.Expiration {
            c.mu.RUnlock()
            return nil, false
        }
    }
    c.mu.RUnlock()
    return item.Object, true
}

func (c *cache) GetWithExpiration(k string) (interface{}, time.Time, bool) {
    c.mu.RLock()
    // "Inlining" of get and Expired
    item, found := c.items[k]
    if !found {
        c.mu.RUnlock()
        return nil, time.Time{}, false
    }

    if item.Expiration > 0 {
        if time.Now().UnixNano() > item.Expiration {
            c.mu.RUnlock()
            return nil, time.Time{}, false
        }

        c.mu.RUnlock()
        return item.Object, time.Unix(0, item.Expiration), true
    }

    c.mu.RUnlock()
    return item.Object, time.Time{}, true
}

func (c *cache) get(k string) (interface{}, bool) {
    item, found := c.items[k]
    if !found {
        return nil, false
    }
    if item.Expiration > 0 {
        if time.Now().UnixNano() > item.Expiration {
            return nil, false
        }
    }
    return item.Object, true
}

func (c *cache) Delete(k string) {
    c.mu.Lock()
    v, evicted := c.delete(k)
    c.mu.Unlock()
    if evicted {
        c.onEvicted(k, v)
    }
}

func (c *cache) delete(k string) (interface{}, bool) {
    if c.onEvicted != nil {
        if v, found := c.items[k]; found {
            delete(c.items, k)
            return v.Object, true
        }
    }
    delete(c.items, k)
    return nil, false
}

type keyAndValue struct {
    key   string
    value interface{}
}

func (c *cache) DeleteExpired() {
    var evictedItems []keyAndValue
    now := time.Now().UnixNano()
    c.mu.Lock()
    for k, v := range c.items {
        // "Inlining" of expired
        if v.Expiration > 0 && now > v.Expiration {
            ov, evicted := c.delete(k)
            if evicted {
                evictedItems = append(evictedItems, keyAndValue{k, ov})
            }
        }
    }
    c.mu.Unlock()
    for _, v := range evictedItems {
        c.onEvicted(v.key, v.value)
    }
}

func (c *cache) OnEvicted(f func(string, interface{})) {
    c.mu.Lock()
    c.onEvicted = f
    c.mu.Unlock()
}

func (c *cache) Save(w io.Writer) (err error) {
    enc := gob.NewEncoder(w)
    defer func() {
        if x := recover(); x != nil {
            err = fmt.Errorf("error registering item types with Gob library")
        }
    }()
    c.mu.RLock()
    defer c.mu.RUnlock()
    for _, v := range c.items {
        gob.Register(v.Object)
    }
    err = enc.Encode(&c.items)
    return
}

func (c *cache) SaveFile(name string) error {
    file, err := os.Create(name)
    if err != nil {
        return err
    }
    err = c.Save(file)
    if err != nil {
        errFile := file.Close()
        if errFile != nil {
            return errFile
        }
        return err
    }
    return file.Close()
}

func (c *cache) Load(r io.Reader) error {
    dec := gob.NewDecoder(r)
    items := map[string]Item{}
    err := dec.Decode(&items)
    if err == nil {
        c.mu.Lock()
        defer c.mu.Unlock()
        for k, v := range items {
            ov, found := c.items[k]
            if !found || ov.Expired() {
                c.items[k] = v
            }
        }
    }
    return err
}

func (c *cache) LoadFile(name string) error {
    fp, err := os.Open(name)
    if err != nil {
        return err
    }
    err = c.Load(fp)
    if err != nil {
        errFile := fp.Close()
        if errFile != nil {
            return errFile
        }
        return err
    }
    return fp.Close()
}

func (c *cache) Items() map[string]Item {
    c.mu.RLock()
    defer c.mu.RUnlock()
    m := make(map[string]Item, len(c.items))
    now := time.Now().UnixNano()
    for k, v := range c.items {
        if v.Expiration > 0 {
            if now > v.Expiration {
                continue
            }
        }
        m[k] = v
    }
    return m
}

func (c *cache) ItemCount() int {
    c.mu.RLock()
    n := len(c.items)
    c.mu.RUnlock()
    return n
}

func (c *cache) Flush() {
    c.mu.Lock()
    c.items = map[string]Item{}
    c.mu.Unlock()
}

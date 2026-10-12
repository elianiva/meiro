// Package lru is a small least-recently-used map.
package lru

// Cache maps keys to values and holds at most a fixed number of them,
// dropping the one used least recently when a new one would overflow it.
// Get, Put and Delete are O(1). The zero value is not usable; call New. A
// Cache is not safe for concurrent use.
type Cache[K comparable, V any] struct {
	limit int
	items map[K]*node[K, V]
	// root is the sentinel of a ring of nodes, most recently used after it.
	root node[K, V]
}

type node[K comparable, V any] struct {
	key        K
	value      V
	prev, next *node[K, V]
}

// New returns a cache that holds at most limit entries. A limit below one
// holds one.
func New[K comparable, V any](limit int) *Cache[K, V] {
	c := &Cache[K, V]{limit: max(limit, 1), items: make(map[K]*node[K, V])}
	c.root.prev, c.root.next = &c.root, &c.root
	return c
}

// Get returns the value under key and marks it used.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.unlink(n)
	c.pushFront(n)
	return n.value, true
}

// Peek returns the value under key without marking it used.
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	return n.value, true
}

// Put stores value under key, marks it used, and drops the least recently
// used entry when the cache is over its limit.
func (c *Cache[K, V]) Put(key K, value V) {
	if n, ok := c.items[key]; ok {
		n.value = value
		c.unlink(n)
		c.pushFront(n)
		return
	}
	n := &node[K, V]{key: key, value: value}
	c.items[key] = n
	c.pushFront(n)
	if len(c.items) > c.limit {
		oldest := c.root.prev
		c.unlink(oldest)
		delete(c.items, oldest.key)
	}
}

// GetOrPut returns the value under key, storing the one create returns when
// there is none yet. Either way the entry is marked used.
func (c *Cache[K, V]) GetOrPut(key K, create func() V) V {
	if value, ok := c.Get(key); ok {
		return value
	}
	value := create()
	c.Put(key, value)
	return value
}

// Delete removes the entry under key, if there is one.
func (c *Cache[K, V]) Delete(key K) {
	if n, ok := c.items[key]; ok {
		c.unlink(n)
		delete(c.items, key)
	}
}

// Clear removes every entry.
func (c *Cache[K, V]) Clear() {
	clear(c.items)
	c.root.prev, c.root.next = &c.root, &c.root
}

// Len is the number of entries held.
func (c *Cache[K, V]) Len() int { return len(c.items) }

func (c *Cache[K, V]) unlink(n *node[K, V]) {
	n.prev.next, n.next.prev = n.next, n.prev
	n.prev, n.next = nil, nil
}

func (c *Cache[K, V]) pushFront(n *node[K, V]) {
	n.prev, n.next = &c.root, c.root.next
	c.root.next.prev = n
	c.root.next = n
}

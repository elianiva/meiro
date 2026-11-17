package lru

type Cache[K comparable, V any] struct {
	limit int
	items map[K]*node[K, V]
	root  node[K, V]
}

type node[K comparable, V any] struct {
	key        K
	value      V
	prev, next *node[K, V]
}

func New[K comparable, V any](limit int) *Cache[K, V] {
	c := &Cache[K, V]{limit: max(limit, 1), items: make(map[K]*node[K, V])}
	c.root.prev, c.root.next = &c.root, &c.root
	return c
}

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

func (c *Cache[K, V]) Peek(key K) (V, bool) {
	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	return n.value, true
}

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

func (c *Cache[K, V]) GetOrPut(key K, create func() V) V {
	if value, ok := c.Get(key); ok {
		return value
	}
	value := create()
	c.Put(key, value)
	return value
}

func (c *Cache[K, V]) Delete(key K) {
	if n, ok := c.items[key]; ok {
		c.unlink(n)
		delete(c.items, key)
	}
}

func (c *Cache[K, V]) Clear() {
	clear(c.items)
	c.root.prev, c.root.next = &c.root, &c.root
}

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

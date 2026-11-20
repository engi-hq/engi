package expression

import (
	"container/list"
	"crypto/sha256"
	"sync"

	"github.com/dop251/goja"
)

// compiledSegment is a literal or a compiled JavaScript block.
type compiledSegment struct {
	literal string
	program *goja.Program // nil for literals
}

// compiledTemplate is the immutable, shareable result of compiling a template.
type compiledTemplate struct {
	segments []compiledSegment
}

// single reports whether the template is exactly one code block, in which case
// the raw value type is preserved.
func (c *compiledTemplate) single() bool {
	return len(c.segments) == 1 && c.segments[0].program != nil
}

type cacheKey [sha256.Size]byte

func keyFor(template string) cacheKey { return sha256.Sum256([]byte(template)) }

// programCache is a fixed-size LRU of compiled templates keyed by template hash.
type programCache struct {
	mu    sync.Mutex
	cap   int
	order *list.List // front = most recently used
	items map[cacheKey]*list.Element
}

type cacheEntry struct {
	key      cacheKey
	template *compiledTemplate
}

func newProgramCache(capacity int) *programCache {
	return &programCache{cap: capacity, order: list.New(), items: make(map[cacheKey]*list.Element, capacity)}
}

func (c *programCache) get(k cacheKey) (*compiledTemplate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).template, true
}

func (c *programCache) put(k cacheKey, t *compiledTemplate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*cacheEntry).template = t
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&cacheEntry{key: k, template: t})
	if c.order.Len() > c.cap {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
}

func (c *programCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

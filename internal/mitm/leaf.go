package mitm

import (
	"container/list"
	"crypto/tls"
	"sync"
	"time"
)

// leafCache is a bounded LRU of leaf certificates keyed by SNI host.
type leafCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	idx map[string]*list.Element
}

type leafEntry struct {
	host string
	cert *tls.Certificate
}

func newLeafCache(max int) *leafCache {
	return &leafCache{max: max, ll: list.New(), idx: make(map[string]*list.Element)}
}

// get returns the cached leaf for host, minting one with mint on a miss or when
// the cached leaf has less than an hour left.
func (c *leafCache) get(host string, now time.Time, mint func(string, time.Time) (*tls.Certificate, error)) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[host]; ok {
		e := el.Value.(*leafEntry)
		if e.cert.Leaf.NotAfter.Sub(now) > time.Hour {
			c.ll.MoveToFront(el)
			return e.cert, nil
		}
		c.ll.Remove(el)
		delete(c.idx, host)
	}
	cert, err := mint(host, now)
	if err != nil {
		return nil, err
	}
	c.idx[host] = c.ll.PushFront(&leafEntry{host: host, cert: cert})
	for c.ll.Len() > c.max {
		back := c.ll.Back()
		c.ll.Remove(back)
		delete(c.idx, back.Value.(*leafEntry).host)
	}
	return cert, nil
}

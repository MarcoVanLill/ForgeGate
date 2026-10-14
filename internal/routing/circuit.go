package routing

import (
	"sync"
	"time"
)

type circuit struct {
	mu        sync.Mutex
	failures  int
	openedAt  time.Time
	threshold int
	cooldown  time.Duration
	now       func() time.Time
}

func newCircuit(threshold int, cooldown time.Duration) *circuit {
	return &circuit{threshold: threshold, cooldown: cooldown, now: time.Now}
}
func (c *circuit) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures < c.threshold {
		return true
	}
	if c.now().Sub(c.openedAt) >= c.cooldown {
		c.failures = c.threshold - 1
		return true
	}
	return false
}
func (c *circuit) success() { c.mu.Lock(); c.failures = 0; c.openedAt = time.Time{}; c.mu.Unlock() }
func (c *circuit) failure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures == c.threshold {
		c.openedAt = c.now()
	}
}
func (c *circuit) open() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures >= c.threshold && c.now().Sub(c.openedAt) < c.cooldown
}

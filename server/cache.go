// Package server 提供 Web 服务模式，允许通过 HTTP GET 请求转换远程模块文件。
//
// 本文件实现带 TTL 的内存缓存，用于缓存转换结果以减少远程文件重复下载。
package server

import (
	"sync"
	"time"
)

// entry 缓存条目。
type entry struct {
	value     string
	expiresAt time.Time
}

// expired 判断条目是否已过期。
func (e *entry) expired() bool {
	return time.Now().After(e.expiresAt)
}

// Cache 带过期时间的线程安全内存缓存。
type Cache struct {
	mu      sync.RWMutex
	items   map[string]*entry
	ttl     time.Duration
	maxSize int
}

// NewCache 创建缓存实例。
// ttl：缓存条目生存时间；maxSize：最大条目数（超出时淘汰最旧的）。
func NewCache(ttl time.Duration, maxSize int) *Cache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 256
	}
	c := &Cache{
		items:   make(map[string]*entry, 64),
		ttl:     ttl,
		maxSize: maxSize,
	}
	// 后台定期清理过期条目
	go c.evictLoop()
	return c
}

// Get 读取缓存，未命中或已过期返回空字符串和 false。
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	if !ok || e.expired() {
		return "", false
	}
	return e.value, true
}

// Put 写入缓存。
func (c *Cache) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 容量超限时淘汰
	if len(c.items) >= c.maxSize {
		c.evictLocked()
	}
	c.items[key] = &entry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// evictLoop 定期清理过期条目。
func (c *Cache) evictLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		c.evictLocked()
		c.mu.Unlock()
	}
}

// evictLocked 在已持锁状态下淘汰过期和最旧条目。
func (c *Cache) evictLocked() {
	// 先删过期
	for k, e := range c.items {
		if e.expired() {
			delete(c.items, k)
		}
	}
	// 仍超容量则按过期时间排序淘汰最旧的
	for len(c.items) > c.maxSize {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, e := range c.items {
			if first || e.expiresAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.expiresAt
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.items, oldestKey)
	}
}

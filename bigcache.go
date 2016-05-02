package bigcache

import (
	"fmt"
	"log"
	"sync"

	"github.com/mikaelnousiainen/bigcache/queue"
)

const (
	minimumEntriesInShard = 10 // Minimum number of entries in single shard
)

// BigCache is fast, concurrent, evicting cache created to keep big number of entries without impact on performance.
// It keeps entries on heap but omits GC for them. To achieve that operations on bytes arrays take place,
// therefore entries (de)serialization in front of the cache will be needed in most use cases.
type BigCache struct {
	shards     []*cacheShard
	lifeWindow uint64
	clock      clock
	hash       Hasher
	config     Config
	shardMask  uint64
	shardSize  int
}

type cacheShard struct {
	hashmap     map[uint64]uint32
	entries     queue.BytesQueue
	lock        sync.RWMutex
	entryBuffer []byte
}

// NewBigCache initialize new instance of BigCache
func NewBigCache(config Config) (*BigCache, error) {
	return newBigCache(config, &systemClock{})
}

func newBigCache(config Config, clock clock) (*BigCache, error) {

	if !isPowerOfTwo(config.Shards) {
		return nil, fmt.Errorf("Shards number must be power of two")
	}

	if config.Hasher == nil {
		config.Hasher = newDefaultHasher()
	}

	cache := &BigCache{
		shards:     make([]*cacheShard, config.Shards),
		lifeWindow: uint64(config.LifeWindow.Seconds()),
		clock:      clock,
		hash:       config.Hasher,
		config:     config,
		shardMask:  uint64(config.Shards - 1),
	}

	cache.shardSize = max(config.MaxEntriesInWindow/config.Shards, minimumEntriesInShard)
	for i := 0; i < config.Shards; i++ {
		cache.shards[i] = &cacheShard{
			hashmap:     make(map[uint64]uint32, cache.shardSize),
			entries:     *queue.NewBytesQueue(cache.shardSize*config.MaxEntrySize, config.Verbose),
			entryBuffer: make([]byte, config.MaxEntrySize+headersSizeInBytes),
		}
	}

	return cache, nil
}

func isPowerOfTwo(number int) bool {
	return (number & (number - 1)) == 0
}

// Get reads entry for the key
func (c *BigCache) Get(key string) ([]byte, error) {
	hashedKey := c.hash.Sum64(key)
	shard := c.getShard(hashedKey)
	shard.lock.RLock()
	defer shard.lock.RUnlock()

	itemIndex := shard.hashmap[hashedKey]

	if itemIndex == 0 {
		return nil, notFound(key)
	}

	wrappedEntry, err := shard.entries.Get(int(itemIndex))
	if err != nil {
		return nil, err
	}
	if entryKey := readKeyFromEntry(wrappedEntry); key != entryKey {
		if c.config.Verbose {
			log.Printf("Collision detected. Both %q and %q have the same hash %x", key, entryKey, hashedKey)
		}
		return nil, notFound(key)
	}
	return readEntry(wrappedEntry), nil
}

// Set saves entry under the key
func (c *BigCache) Set(key string, entry []byte) {
	hashedKey := c.hash.Sum64(key)
	shard := c.getShard(hashedKey)
	shard.lock.Lock()
	defer shard.lock.Unlock()

	currentTimestamp := uint64(c.clock.epoch())

	if previousIndex := shard.hashmap[hashedKey]; previousIndex != 0 {
		if previousEntry, err := shard.entries.Get(int(previousIndex)); err == nil {
			resetKeyFromEntry(previousEntry)
		}
	}

	if oldestEntry, err := shard.entries.Peek(); err == nil {
		c.onEvict(oldestEntry, currentTimestamp, func() {
			shard.entries.Pop()
			hash := readHashFromEntry(oldestEntry)
			delete(shard.hashmap, hash)
		})
	}

	w := wrapEntry(currentTimestamp, hashedKey, key, entry, &shard.entryBuffer)
	index := shard.entries.Push(w)
	shard.hashmap[hashedKey] = uint32(index)
}

// Clear deletes all entries in all shards
func (c *BigCache) Clear() {
	for _, shard := range c.shards {
		shard.lock.Lock()
		shard.entries.Clear()
		shard.hashmap = make(map[uint64]uint32, c.shardSize)
		shard.lock.Unlock()
	}
}

// Iterate calls the accept function for all key-value pairs in all shards.
// Note that the implementation is not thread-safe
func (c *BigCache) Iterate(accept func (string, []byte)) {
	for _, shard := range c.shards {
		for hashedKey, _ := range shard.hashmap {
			key, value, err := c.getKeyAndValue(shard, hashedKey)
			if err != nil {
				continue
			}

			accept(key, value)
		}
	}
}

func (c* BigCache) Size() uint64 {
	var count uint64
	for _, shard := range c.shards {
		count += uint64(len(shard.hashmap))
	}

	return count
}

func (c *BigCache) getKeyAndValue(shard *cacheShard, hashedKey uint64) (string, []byte, error) {
	itemIndex := shard.hashmap[hashedKey]

	if itemIndex == 0 {
		return "", nil, notFound("")
	}

	wrappedEntry, err := shard.entries.Get(int(itemIndex))
	if err != nil {
		return "", nil, err
	}

	entryKey := readKeyFromEntry(wrappedEntry)

	return entryKey, readEntry(wrappedEntry), nil
}

func (c *BigCache) onEvict(oldestEntry []byte, currentTimestamp uint64, evict func()) {
	oldestTimestamp := readTimestampFromEntry(oldestEntry)
	if currentTimestamp-oldestTimestamp > c.lifeWindow {
		evict()
	}
}

func (c *BigCache) getShard(hashedKey uint64) (shard *cacheShard) {
	return c.shards[hashedKey&c.shardMask]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

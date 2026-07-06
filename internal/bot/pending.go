package bot

import (
	"strconv"
	"sync"
	"time"

	"visits/internal/parse"
)

// pendingStore holds parsed-but-unconfirmed appointments between the parse
// message and the Save/Cancel tap. In-memory only: a restart drops pending
// confirmations, which is harmless — the user just re-sends the text.
type pendingStore struct {
	mu    sync.Mutex
	seq   int64
	items map[string]pendingEntry
}

type pendingEntry struct {
	parsed  []parse.Parsed
	created time.Time
}

func newPendingStore() *pendingStore {
	return &pendingStore{items: make(map[string]pendingEntry)}
}

// put stores parsed items and returns a short key to embed in callback data.
// now is passed in (no Date.now in this codebase's spirit of testability) to
// stamp the entry for opportunistic eviction.
func (p *pendingStore) put(parsed []parse.Parsed, now time.Time) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked(now)
	p.seq++
	key := strconv.FormatInt(p.seq, 36)
	p.items[key] = pendingEntry{parsed: parsed, created: now}
	return key
}

func (p *pendingStore) take(key string) ([]parse.Parsed, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.items[key]
	if ok {
		delete(p.items, key)
	}
	return e.parsed, ok
}

// evictLocked drops entries older than an hour; a stale confirmation card is
// no longer actionable anyway.
func (p *pendingStore) evictLocked(now time.Time) {
	for k, e := range p.items {
		if now.Sub(e.created) > time.Hour {
			delete(p.items, k)
		}
	}
}

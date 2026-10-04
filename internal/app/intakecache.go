package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// intakeCacheTTL is how long a helpdesk→tracker pair is remembered. A day
// covers a shift of pasting the same ticket into the composer, the CLI and
// a steer, and is short enough that a ticket re-linked to another issue is
// picked up by tomorrow.
const intakeCacheTTL = 24 * time.Hour

// intakeCacheFile is where the pairs are kept, beside the runs they lead to.
const intakeCacheFile = "intake-cache.json"

// intakeCacheEntry is one remembered pair: the key, how it was found, and
// the helpdesk number when the lookup started from an id.
type intakeCacheEntry struct {
	Key    string    `json:"key"`
	How    string    `json:"how"`
	Number string    `json:"number,omitempty"`
	At     time.Time `json:"at"`
}

// intakeCache remembers helpdesk→tracker pairs in a workspace's
// .sirdar/runs/intake-cache.json, keyed by helpdesk number ("n:28310") and
// by record id ("id:…"), because the composer may be handed either. It
// holds no secret and nothing the tracker does not already say. A nil cache
// remembers nothing.
type intakeCache struct {
	path string
	now  func() time.Time
}

// intakeCacheMu serialises every read-modify-write of every cache file in
// the process; one composer, one CLI and one HTTP route are the writers, so
// one lock is plenty.
var intakeCacheMu sync.Mutex

func newIntakeCache(root string, now func() time.Time) *intakeCache {
	if root == "" {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &intakeCache{path: filepath.Join(root, ".sirdar", "runs", intakeCacheFile), now: now}
}

func (c *intakeCache) read() map[string]intakeCacheEntry {
	out := map[string]intakeCacheEntry{}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return out
	}
	// A cache that does not parse is a cache that is empty.
	_ = json.Unmarshal(data, &out)
	return out
}

func cacheKeys(number, id string) []string {
	var keys []string
	if number != "" {
		keys = append(keys, "n:"+number)
	}
	if id != "" {
		keys = append(keys, "id:"+id)
	}
	return keys
}

func (c *intakeCache) get(number, id string) (intakeCacheEntry, bool) {
	if c == nil {
		return intakeCacheEntry{}, false
	}
	intakeCacheMu.Lock()
	defer intakeCacheMu.Unlock()
	all := c.read()
	for _, k := range cacheKeys(number, id) {
		if e, ok := all[k]; ok && c.now().Sub(e.At) < intakeCacheTTL && e.Key != "" {
			return e, true
		}
	}
	return intakeCacheEntry{}, false
}

func (c *intakeCache) put(number, id string, e intakeCacheEntry) {
	if c == nil {
		return
	}
	intakeCacheMu.Lock()
	defer intakeCacheMu.Unlock()
	all := c.read()
	now := c.now()
	for k, old := range all {
		if now.Sub(old.At) >= intakeCacheTTL {
			delete(all, k)
		}
	}
	e.At = now
	for _, k := range cacheKeys(number, id) {
		all[k] = e
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.path)
}

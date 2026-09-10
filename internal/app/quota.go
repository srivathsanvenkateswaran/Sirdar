package app

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// quotaWindow is the shape of the provider payloads a quota reading is
// derived from: Claude's `rate_limit_event` line and Codex's
// `account/rateLimits/updated` notification, exactly as the adapters
// recorded them in payload.raw.
type rawQuota struct {
	RateLimitInfo *struct {
		ResetsAt       int64 `json:"resetsAt"`
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`

	Params *struct {
		RateLimits *struct {
			Primary *struct {
				UsedPercent float64 `json:"usedPercent"`
				ResetsAt    int64   `json:"resetsAt"`
			} `json:"primary"`
		} `json:"rateLimits"`
	} `json:"params"`
}

// quotaKinds are the recorded event kinds that can carry a rate-limit
// reading: Codex reports one on every turn as a system event, and both
// providers report one as rate_limited when the window is exhausted.
var quotaKinds = map[string]bool{"rate_limited": true, "system": true}

// quotaHistory is how far back the startup scan of the event logs reaches.
const quotaHistory = 24 * time.Hour

// unixTime renders a provider's unix-second timestamp, and zero as empty.
func unixTime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return wireTime(time.Unix(sec, 0))
}

// parseQuota derives a quota reading from one recorded raw provider line.
// The shape decides the provider: only Claude sends rate_limit_info, only
// Codex sends params.rateLimits.
func parseQuota(raw json.RawMessage) (Quota, bool) {
	if len(raw) == 0 {
		return Quota{}, false
	}
	var r rawQuota
	if err := json.Unmarshal(raw, &r); err != nil {
		return Quota{}, false
	}

	if info := r.RateLimitInfo; info != nil && len(info.UnifiedWindows) > 0 {
		q := Quota{Provider: "claude"}
		if w, ok := info.UnifiedWindows["five_hour"]; ok {
			q.FiveHour = &QuotaWindow{Utilization: w.Utilization, ResetsAt: unixTime(w.ResetsAt)}
		}
		if w, ok := info.UnifiedWindows["seven_day"]; ok {
			q.SevenDay = &QuotaWindow{Utilization: w.Utilization, ResetsAt: unixTime(w.ResetsAt)}
		}
		if q.FiveHour == nil && q.SevenDay == nil {
			return Quota{}, false
		}
		return q, true
	}

	if r.Params != nil && r.Params.RateLimits != nil && r.Params.RateLimits.Primary != nil {
		p := r.Params.RateLimits.Primary
		used := p.UsedPercent
		return Quota{Provider: "codex", UsedPercent: &used, ResetsAt: unixTime(p.ResetsAt)}, true
	}

	return Quota{}, false
}

// sameReading reports whether two quotas carry the same numbers, ignoring
// when they were observed. It decides whether a reading is worth an event.
func sameReading(a, b Quota) bool {
	if a.Provider != b.Provider || a.ResetsAt != b.ResetsAt {
		return false
	}
	if !sameWindow(a.FiveHour, b.FiveHour) || !sameWindow(a.SevenDay, b.SevenDay) {
		return false
	}
	switch {
	case a.UsedPercent == nil && b.UsedPercent == nil:
		return true
	case a.UsedPercent == nil || b.UsedPercent == nil:
		return false
	default:
		return *a.UsedPercent == *b.UsedPercent
	}
}

func sameWindow(a, b *QuotaWindow) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// quotaTracker keeps the newest rate-limit reading per provider.
type quotaTracker struct {
	mu      sync.Mutex
	newest  map[string]Quota
	seenAt  map[string]time.Time
	nowFunc func() time.Time
}

func newQuotaTracker() *quotaTracker {
	return &quotaTracker{newest: map[string]Quota{}, seenAt: map[string]time.Time{}}
}

func (t *quotaTracker) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return time.Now()
}

// observe feeds one recorded event to the tracker. It returns the stored
// quota and true only when the reading is newer than what is held and says
// something different.
func (t *quotaTracker) observe(ev RunEvent) (Quota, bool) {
	if !quotaKinds[ev.Kind] {
		return Quota{}, false
	}
	q, ok := parseQuota(ev.Payload.Raw)
	if !ok {
		return Quota{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, ev.T)
	if err != nil {
		at = t.now()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.newest[q.Provider]; ok {
		if at.Before(t.seenAt[q.Provider]) {
			return Quota{}, false
		}
		if sameReading(prev, q) {
			t.seenAt[q.Provider] = at
			return Quota{}, false
		}
	}
	q.ObservedAt = wireTime(at)
	t.newest[q.Provider] = q
	t.seenAt[q.Provider] = at
	return q, true
}

// snapshot returns the current reading for every provider, provider order.
func (t *quotaTracker) snapshot() []Quota {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Quota, 0, len(t.newest))
	for _, q := range t.newest {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// scanRoots reads the event logs already on disk in every workspace and
// feeds the readings recorded since `since` to the tracker, so the quota
// meter is populated before any new run starts. It returns the readings
// that changed.
func (t *quotaTracker) scanRoots(roots []string, since time.Time) []Quota {
	var changed []Quota
	for _, root := range roots {
		logs, err := filepath.Glob(filepath.Join(root, ".sirdar", "runs", "*", "*", "events.jsonl"))
		if err != nil {
			continue
		}
		for _, path := range logs {
			if fi, err := os.Stat(path); err != nil || fi.ModTime().Before(since) {
				continue
			}
			changed = append(changed, t.scanLog(path, since)...)
		}
	}
	return changed
}

func (t *quotaTracker) scanLog(path string, since time.Time) []Quota {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var changed []Quota
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var ev RunEvent
			if json.Unmarshal(line, &ev) == nil {
				if at, perr := time.Parse(time.RFC3339Nano, ev.T); perr != nil || !at.Before(since) {
					if q, ok := t.observe(ev); ok {
						changed = append(changed, q)
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				return changed
			}
			return changed
		}
	}
}

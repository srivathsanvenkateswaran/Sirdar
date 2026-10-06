package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// shadowLockInfo is what .sirdar/shadow.lock says about its holder.
type shadowLockInfo struct {
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	By        string `json:"by"` // serve, watch or poll
	StartedAt string `json:"startedAt"`
}

// shadowLockStaleAfter is how many poll intervals a lock may go untouched
// before it is taken as abandoned. A holder touches it on every supervise
// pass, so only a holder that stopped running, or a host whose clock
// jumped, misses three intervals.
const shadowLockStaleAfter = 3

func shadowLockPath(root string) string {
	return filepath.Join(root, ".sirdar", "shadow.lock")
}

func hostName() string {
	h, _ := os.Hostname()
	return h
}

// readShadowLock reads the lock and its mtime. ok is false when there is
// no lock. A lock whose contents do not parse is still a lock: it may be
// one another process created a moment ago and has not written yet.
func readShadowLock(root string) (info shadowLockInfo, mtime time.Time, ok bool, err error) {
	path := shadowLockPath(root)
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return shadowLockInfo{}, time.Time{}, false, nil
	}
	if err != nil {
		return shadowLockInfo{}, time.Time{}, false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return shadowLockInfo{}, time.Time{}, false, nil
	}
	if err != nil {
		return shadowLockInfo{}, time.Time{}, false, err
	}
	_ = json.Unmarshal(raw, &info)
	return info, fi.ModTime(), true, nil
}

// shadowLockStale reports whether a lock's holder is gone: a process on
// this host that no longer exists, or any holder that has not touched the
// lock for three intervals.
func shadowLockStale(info shadowLockInfo, mtime, now time.Time, interval time.Duration) bool {
	if info.Host != "" && info.Host == hostName() && !pidAlive(info.PID) {
		return true
	}
	return now.Sub(mtime) > shadowLockStaleAfter*interval
}

// shadowLockHeldElsewhere is the reason a status gives for a live lock.
func shadowLockHeldElsewhere(info shadowLockInfo) string {
	return fmt.Sprintf("shadow: watched by pid %d (%s)", info.PID, info.By)
}

// takeShadowLock creates the lock for this process, taking over a stale
// one. held is false, with the holder, when a live process has it.
func takeShadowLock(root, by string, now time.Time, interval time.Duration) (held bool, holder shadowLockInfo, err error) {
	path := shadowLockPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, shadowLockInfo{}, fmt.Errorf("app: shadow lock: %w", err)
	}
	// Two tries: the second follows removing a stale lock, and losing that
	// race to another process taking it over is an ordinary refusal.
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			info := shadowLockInfo{PID: os.Getpid(), Host: hostName(), By: by, StartedAt: now.Format(time.RFC3339)}
			raw, _ := json.Marshal(info)
			_, werr := f.Write(raw)
			cerr := f.Close()
			if werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return false, shadowLockInfo{}, fmt.Errorf("app: shadow lock: %w", werr)
			}
			// The mtime is what staleness is measured by, against the
			// same clock every later touch uses.
			_ = os.Chtimes(path, now, now)
			return true, info, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, shadowLockInfo{}, fmt.Errorf("app: shadow lock: %w", err)
		}
		info, mtime, ok, err := readShadowLock(root)
		if err != nil {
			return false, shadowLockInfo{}, fmt.Errorf("app: shadow lock: %w", err)
		}
		if ok && !shadowLockStale(info, mtime, now, interval) {
			return false, info, nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, shadowLockInfo{}, fmt.Errorf("app: shadow lock: %w", err)
		}
	}
	info, _, _, _ := readShadowLock(root)
	return false, info, nil
}

// touchShadowLock marks the lock as still held.
func touchShadowLock(root string, now time.Time) error {
	return os.Chtimes(shadowLockPath(root), now, now)
}

// ownsShadowLock reports whether the lock on disk is this process's.
func ownsShadowLock(root string) bool {
	info, _, ok, err := readShadowLock(root)
	return err == nil && ok && info.PID == os.Getpid() && info.Host == hostName()
}

// releaseShadowLock removes the lock when it is this process's. One that
// another process took over after this one let it go stale is theirs now.
func releaseShadowLock(root string) {
	if ownsShadowLock(root) {
		os.Remove(shadowLockPath(root))
	}
}

package terrain

import (
	"container/list"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// memLRU keeps the most recently used decoded tiles. It is not safe for
// concurrent use; the Service guards it with its mutex.
type memLRU struct {
	max   int
	order *list.List // front is most recent; values are *memEntry
	byID  map[tileID]*list.Element
}

type memEntry struct {
	id tileID
	r  *raster
}

func newMemLRU(max int) *memLRU {
	return &memLRU{max: max, order: list.New(), byID: map[tileID]*list.Element{}}
}

func (m *memLRU) get(id tileID) *raster {
	el, ok := m.byID[id]
	if !ok {
		return nil
	}
	m.order.MoveToFront(el)
	return el.Value.(*memEntry).r
}

func (m *memLRU) put(id tileID, r *raster) {
	if el, ok := m.byID[id]; ok {
		el.Value.(*memEntry).r = r
		m.order.MoveToFront(el)
		return
	}
	m.byID[id] = m.order.PushFront(&memEntry{id: id, r: r})
	for m.order.Len() > m.max {
		last := m.order.Back()
		m.order.Remove(last)
		delete(m.byID, last.Value.(*memEntry).id)
	}
}

// diskEntry is a tile file in the cache directory.
type diskEntry struct {
	size int64
	used time.Time // last access; also kept as the file's modification time
}

func (s *Service) tilePath(id tileID) string {
	return filepath.Join(s.cfg.CacheDir, id.key()+".tif")
}

// openCacheDir creates the cache directory and takes stock of the tiles in
// it. Leftover .part files are not tiles: those older than a fetch could
// take are removed, and none is ever read.
func (s *Service) openCacheDir() {
	if err := os.MkdirAll(s.cfg.CacheDir, 0o755); err != nil {
		s.markUnwritable(err)
		return
	}
	entries, err := os.ReadDir(s.cfg.CacheDir)
	if err != nil {
		s.markUnwritable(err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if strings.HasSuffix(name, ".part") {
			if s.now().Sub(info.ModTime()) > s.cfg.FetchTimeout {
				_ = os.Remove(filepath.Join(s.cfg.CacheDir, name))
			}
			continue
		}
		key, ok := strings.CutSuffix(name, ".tif")
		if !ok {
			continue
		}
		if id, ok := parseTileKey(key); ok {
			s.disk[id] = &diskEntry{size: info.Size(), used: info.ModTime()}
			s.diskBytes += info.Size()
		}
	}
	s.evictLocked(tileID{lat: 1000}) // no tile is exempt
}

// markUnwritable switches the service to serving from memory only, with one
// warning for the life of the service.
func (s *Service) markUnwritable(err error) {
	s.mu.Lock()
	s.diskOK = false
	s.mu.Unlock()
	s.warnOnce.Do(func() {
		s.log.Warn("terrain cache directory not writable; serving tiles from memory only",
			zap.String("dir", s.cfg.CacheDir), zap.Error(err))
	})
}

// writeAtomic writes data to path through path.part, so a crash never leaves
// a truncated file under the real name.
func writeAtomic(path string, data []byte) error {
	part := path + ".part"
	if err := os.WriteFile(part, data, 0o644); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, path); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

// storeTile saves a downloaded tile in the disk cache, if there is one, and
// evicts the least recently used tiles to keep under MaxCacheBytes.
func (s *Service) storeTile(id tileID, data []byte) {
	s.mu.Lock()
	ok := s.diskOK
	s.mu.Unlock()
	if !ok {
		return
	}
	if err := writeAtomic(s.tilePath(id), data); err != nil {
		s.markUnwritable(err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.disk[id]; ok {
		s.diskBytes -= old.size
	}
	s.disk[id] = &diskEntry{size: int64(len(data)), used: s.now()}
	s.diskBytes += int64(len(data))
	s.evictLocked(id)
}

// touch records a use of a cached tile, in memory and on the file itself so
// the order survives a restart. Callers hold no lock.
func (s *Service) touch(id tileID) {
	now := s.now()
	s.mu.Lock()
	e, ok := s.disk[id]
	if ok {
		e.used = now
	}
	s.mu.Unlock()
	if ok {
		_ = os.Chtimes(s.tilePath(id), now, now)
	}
}

// removeTile drops a tile from the disk cache.
func (s *Service) removeTile(id tileID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(id)
}

func (s *Service) removeLocked(id tileID) {
	if e, ok := s.disk[id]; ok {
		s.diskBytes -= e.size
		delete(s.disk, id)
	}
	_ = os.Remove(s.tilePath(id))
}

// evictLocked removes least recently used tiles until the cache fits. It
// spares keep and any tile being loaded right now.
func (s *Service) evictLocked(keep tileID) {
	for s.diskBytes > s.cfg.MaxCacheBytes {
		var victim tileID
		var oldest time.Time
		found := false
		for id, e := range s.disk {
			if id == keep || s.loading[id] != nil {
				continue
			}
			if !found || e.used.Before(oldest) {
				victim, oldest, found = id, e.used, true
			}
		}
		if !found {
			return
		}
		s.log.Debug("terrain cache evicting tile", zap.String("tile", victim.key()))
		s.removeLocked(victim)
	}
}

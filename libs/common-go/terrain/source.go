package terrain

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

const (
	// DefaultSourceURL is the public Copernicus GLO-30 bucket. Each tile is
	// <key>/<key>.tif and the bucket root holds tileList.txt.
	DefaultSourceURL = "https://copernicus-dem-30m.s3.amazonaws.com"

	tileListName = "tileList.txt"
	maxTileBytes = 256 << 20 // a GLO-30 tile is under 100 MB
	maxListBytes = 16 << 20  // the list is about 1 MB
)

// fetch GETs a path below the source URL. It runs on its own deadline,
// FetchTimeout, not on any caller's context: a download that a caller stops
// waiting for still completes and lands in the cache.
func (s *Service) fetch(path string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.FetchTimeout)
	defer cancel()
	url := s.cfg.SourceURL + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("terrain: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("terrain: GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("terrain: GET %s: %w", url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("terrain: GET %s: larger than %d bytes", url, limit)
	}
	return body, nil
}

func tileURLPath(id tileID) string {
	k := id.key()
	return k + "/" + k + ".tif"
}

// parseTileList reads tileList.txt: one tile key per line, CRLF line ends.
func parseTileList(b []byte) (map[tileID]struct{}, error) {
	idx := map[tileID]struct{}{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if id, ok := parseTileKey(strings.TrimSpace(sc.Text())); ok {
			idx[id] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("terrain: %s: %w", tileListName, err)
	}
	if len(idx) == 0 {
		return nil, errors.New("terrain: " + tileListName + " lists no tiles")
	}
	return idx, nil
}

// loadIndex returns the tile list, from the cache directory when it is there
// and otherwise from the source, saving it for next time.
func (s *Service) loadIndex() (map[tileID]struct{}, error) {
	path := filepath.Join(s.cfg.CacheDir, tileListName)
	if b, err := os.ReadFile(path); err == nil {
		if idx, err := parseTileList(b); err == nil {
			return idx, nil
		}
		s.log.Warn("cached terrain tile list unreadable; fetching it again", zap.String("path", path))
	}
	b, err := s.fetch(tileListName, maxListBytes)
	if err != nil {
		return nil, err
	}
	idx, err := parseTileList(b)
	if err != nil {
		return nil, err
	}
	if s.canWrite() {
		if err := writeAtomic(path, b); err != nil {
			s.markUnwritable(err)
		} else {
			s.markWritable()
		}
	}
	return idx, nil
}

// loadTile produces a decoded tile: from the disk cache if it is there and
// decodes, otherwise downloaded. The cache folder is read even while it
// cannot be written, and a file another process put there (terrain-preload)
// is taken up. A cached file that fails to decode is deleted and fetched
// again, once. A download that fails to decode is an error and is not kept.
// At most MaxDownloads tiles download and decode at once.
func (s *Service) loadTile(id tileID) (*raster, error) {
	if s.onDisk(id) {
		r, err := s.decodeCached(id)
		if err == nil {
			s.touch(id)
			return r, nil
		}
		s.log.Warn("cached terrain tile unreadable; fetching it again",
			zap.String("tile", id.key()), zap.Error(err))
		s.removeTile(id)
	}

	s.downloads <- struct{}{}
	defer func() { <-s.downloads }()
	data, err := s.fetch(tileURLPath(id), maxTileBytes)
	if err != nil {
		return nil, err
	}
	r, err := decodeTIFF(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("terrain: %s: %w", id.key(), err)
	}
	s.downloaded.Add(1)
	s.storeTile(id, data)
	return r, nil
}

func (s *Service) decodeCached(id tileID) (*raster, error) {
	f, err := os.Open(s.tilePath(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return decodeTIFF(f, info.Size())
}

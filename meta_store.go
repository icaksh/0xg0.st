package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/golang/glog"
)

type MetaStore interface {
	Init() error
	Save(id string, meta fileMeta) error
	Get(id string) (fileMeta, bool, error)
	Delete(id string) error
	ListExpired(nowMs int64, limit int) ([]string, error)
}

type fileMetaStore struct{}

func (s *fileMetaStore) Init() error {
	return nil
}

func (s *fileMetaStore) Save(id string, meta fileMeta) error {
	return writeMetaFile(storagePathForID(id), meta)
}

func (s *fileMetaStore) Get(id string) (fileMeta, bool, error) {
	return readMetaFile(storagePathForID(id))
}

func (s *fileMetaStore) Delete(id string) error {
	return nil
}

func (s *fileMetaStore) ListExpired(nowMs int64, limit int) ([]string, error) {
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return nil, nil
	}

	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, ok, err := readMetaFile(storagePathForID(id))
		if err != nil {
			glog.Errorf("Error reading metadata for %s: %s", id, err.Error())
			continue
		}
		if !ok || meta.ExpiresAtMs <= 0 {
			continue
		}
		if nowMs > meta.ExpiresAtMs {
			ids = append(ids, id)
			if limit > 0 && len(ids) >= limit {
				return ids, nil
			}
		}
	}
	return ids, nil
}

func storagePathForID(id string) string {
	return filepath.Join(storageDir, id)
}

func purgeExpiredOnce() {
	nowMs := time.Now().UnixMilli()
	ids, err := metaStore.ListExpired(nowMs, 0)
	if err != nil {
		glog.Errorf("Error listing expired files: %s", err.Error())
		return
	}
	for _, id := range ids {
		_ = os.RemoveAll(storagePathForID(id))
		_ = metaStore.Delete(id)
	}
}

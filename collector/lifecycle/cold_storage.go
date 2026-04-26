package lifecycle

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrCrossDeviceRename = errors.New("cross-device cold storage rename is not atomic")

type ColdObjectInfo struct {
	URI  string
	Size int64
}

type ColdStorage interface {
	Put(uri string, data []byte) error
	PutReader(uri string, r io.Reader) error
	Get(uri string) (io.ReadCloser, error)
	Exists(uri string) (bool, error)
	Stat(uri string) (ColdObjectInfo, error)
	Delete(uri string) error
	List(prefix string) ([]string, error)
	Rename(src, dst string) error
}

type LocalColdStorage struct {
	root     string
	deviceID func(path string) (uint64, error)
}

func NewLocalColdStorage(root string) *LocalColdStorage {
	return &LocalColdStorage{
		root:     root,
		deviceID: filesystemDeviceID,
	}
}

func (s *LocalColdStorage) Put(uri string, data []byte) error {
	return s.PutReader(uri, bytes.NewReader(data))
}

func (s *LocalColdStorage) StagingDir(uri string) (string, error) {
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func (s *LocalColdStorage) PutReader(uri string, r io.Reader) error {
	if r == nil {
		return errors.New("cold storage reader is required")
	}
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *LocalColdStorage) Get(uri string) (io.ReadCloser, error) {
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *LocalColdStorage) Exists(uri string) (bool, error) {
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (s *LocalColdStorage) Stat(uri string) (ColdObjectInfo, error) {
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return ColdObjectInfo{}, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return ColdObjectInfo{}, err
	}
	return ColdObjectInfo{URI: uri, Size: stat.Size()}, nil
}

func (s *LocalColdStorage) Delete(uri string) error {
	path, err := LocalPathForColdURI(s.root, uri)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *LocalColdStorage) List(prefix string) ([]string, error) {
	prefixPath, err := LocalPathForColdURI(s.root, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	if _, err := os.Stat(prefixPath); errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	err = filepath.WalkDir(prefixPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return nil
		}
		out = append(out, FormatColdURI(ColdURI{Dataset: parts[0], Path: strings.Join(parts[1:], "/")}))
		return nil
	})
	sort.Strings(out)
	return out, err
}

func (s *LocalColdStorage) Rename(src, dst string) error {
	srcPath, err := LocalPathForColdURI(s.root, src)
	if err != nil {
		return err
	}
	dstPath, err := LocalPathForColdURI(s.root, dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	srcDev, err := s.deviceID(srcPath)
	if err != nil {
		return err
	}
	dstDev, err := s.deviceID(filepath.Dir(dstPath))
	if err != nil {
		return err
	}
	if srcDev != dstDev {
		return ErrCrossDeviceRename
	}
	return os.Rename(srcPath, dstPath)
}

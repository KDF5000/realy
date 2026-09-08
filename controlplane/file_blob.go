package controlplane

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileBlobStore keeps artifacts outside PostgreSQL while metadata remains durable.
type FileBlobStore struct{ Root string }

func NewFileBlobStore(root string) (*FileBlobStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("realy blob: root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &FileBlobStore{Root: root}, nil
}

func (s *FileBlobStore) path(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, `/\\`) || key == "." || key == ".." {
		return "", errors.New("realy blob: invalid key")
	}
	return filepath.Join(s.Root, key), nil
}

func (s *FileBlobStore) Put(ctx context.Context, key string, reader io.Reader) (int64, error) {
	path, err := s.path(key)
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(s.Root, ".upload-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	written, copyErr := io.Copy(tmp, &contextReader{ctx: ctx, reader: reader})
	closeErr := tmp.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		return 0, err
	}
	return written, nil
}

func (s *FileBlobStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	value, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return value, err
}

func (s *FileBlobStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}

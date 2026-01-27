package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"tweet-audit/internal/tweets/model"
	"tweet-audit/internal/tweets/util"
)

type localStore struct {
	baseDir string
}

func NewLocalFileStore(baseDir string) model.FileStore {
	// Ensure directory exists (ignore error - will fail on Save if can't create)
	_ = os.MkdirAll(baseDir, 0o755)
	return &localStore{baseDir: baseDir}
}

func (s *localStore) Save(ctx context.Context, name string, r io.Reader) (*model.StoredFile, error) {

	// Create temporary file for atomic write
	tmp, err := os.CreateTemp(s.baseDir, "upload-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	defer tmp.Close()

	// Copy contents to temp file
	size, err := io.Copy(tmp, r)
	if err != nil {
		return nil, fmt.Errorf("failed to copy file contents: %w", err)
	}

	// generate an id
	id := util.GenID()

	// rename file with the id name and its original extension.
	finalName := id + filepath.Ext(name)

	// final path is gotten as a join of base directory and the final name.
	finalPath := filepath.Join(s.baseDir, finalName)

	if err := os.Rename(tmp.Name(), finalPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	sf := &model.StoredFile{
		ID:           id,
		Size:         size,
		OriginalName: name,
		CreatedAt:    time.Now(),
	}

	return sf, nil
}

func (s *localStore) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	// 1. Sanitize the ID to prevent path traversal
	if !filepath.IsLocal(id) { // ensure the id is a local filesystem path
		return nil, fmt.Errorf("invalid id")
	}

	pattern := filepath.Join(s.baseDir, id+"*") // matches any file starting with the id irrespective of ext.

	// 3. Find the matches
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, os.ErrNotExist
	}

	return os.Open(matches[0])
}

func (s *localStore) Delete(ctx context.Context, id string) error {
	if !filepath.IsLocal(id) { // we check if the id is a local filesystem path
		return fmt.Errorf("invalid id")
	}
	pattern := filepath.Join(s.baseDir, id+"*") // matches any file starting with the id irrespective of ext.
	matches, err := filepath.Glob(pattern)      // find all matches
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return os.ErrNotExist
	}

	// we had to delete all matches because we can have many different extensions for same id.
	for i := 0; i < len(matches); i++ {
		if err := os.Remove(matches[i]); err != nil {
			return err
		}
	}
	return nil
}

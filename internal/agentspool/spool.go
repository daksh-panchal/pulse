package agentspool

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daksh-panchal/pulse/internal/telemetry"
)

var ErrFull = errors.New("telemetry spool is full")

type Spool struct {
	directory string
	maximum   int
}

type Item struct {
	Path  string
	Batch telemetry.Batch
}

func Open(directory string, maximum int) (*Spool, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("spool directory must not be empty")
	}
	if maximum < 1 {
		return nil, errors.New("spool maximum must be positive")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create telemetry spool: %w", err)
	}
	return &Spool{directory: directory, maximum: maximum}, nil
}

func (spool *Spool) Enqueue(batch telemetry.Batch) error {
	target := filepath.Join(spool.directory, fileName(batch))
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect queued batch: %w", err)
	}
	items, err := spool.Pending()
	if err != nil {
		return err
	}
	if len(items) >= spool.maximum {
		return ErrFull
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("encode queued batch: %w", err)
	}
	temporary, err := os.CreateTemp(spool.directory, ".pulse-batch-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary queued batch: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect queued batch: %w", err)
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write queued batch: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync queued batch: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close queued batch: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("publish queued batch: %w", err)
	}
	return nil
}

func (spool *Spool) Pending() ([]Item, error) {
	entries, err := os.ReadDir(spool.directory)
	if err != nil {
		return nil, fmt.Errorf("read telemetry spool: %w", err)
	}
	items := make([]Item, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(spool.directory, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open queued batch: %w", err)
		}
		var batch telemetry.Batch
		decodeErr := json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&batch)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode queued batch %s: %w", entry.Name(), decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close queued batch %s: %w", entry.Name(), closeErr)
		}
		items = append(items, Item{Path: path, Batch: batch})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Batch.SentAt.Equal(items[j].Batch.SentAt) {
			return items[i].Batch.Sequence < items[j].Batch.Sequence
		}
		return items[i].Batch.SentAt.Before(items[j].Batch.SentAt)
	})
	return items, nil
}

func (spool *Spool) Remove(item Item) error {
	cleanDirectory := filepath.Clean(spool.directory) + string(os.PathSeparator)
	cleanPath := filepath.Clean(item.Path)
	if !strings.HasPrefix(cleanPath, cleanDirectory) {
		return errors.New("queued batch path is outside the spool")
	}
	if err := os.Remove(cleanPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove delivered batch: %w", err)
	}
	return nil
}

func (spool *Spool) NextSequence(fallback int64) (int64, error) {
	items, err := spool.Pending()
	if err != nil {
		return 0, err
	}
	next := fallback
	for _, item := range items {
		if item.Batch.Sequence >= next {
			next = item.Batch.Sequence + 1
		}
	}
	return next, nil
}

func fileName(batch telemetry.Batch) string {
	return fmt.Sprintf("%s-%020d.json", batch.Agent.ID, batch.Sequence)
}

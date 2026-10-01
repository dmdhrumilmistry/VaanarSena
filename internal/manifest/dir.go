package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirLoader applies every *.yaml, *.yml and *.json file under Dir, on start
// and then periodically. Mount a Git checkout or a Kubernetes ConfigMap there
// for GitOps. Unchanged content is skipped, so the loop is cheap.
type DirLoader struct {
	Applier *Applier
	Dir     string
	Owner   string
	Prune   bool
	Log     *slog.Logger
	last    string
}

// Load reads and applies the directory once. All files are validated as one
// set, so a broken file stops the whole apply instead of half applying it.
func (l *DirLoader) Load(ctx context.Context) error {
	var files []string
	err := filepath.WalkDir(l.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Kubernetes ConfigMap mounts expose ..data symlinks and timestamped
		// directories; read only the visible files.
		if d.IsDir() && strings.HasPrefix(d.Name(), "..") {
			return filepath.SkipDir
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml", ".json":
			if !strings.HasPrefix(d.Name(), ".") {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	h := sha256.New()
	var all []Resource
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		h.Write([]byte(f))
		h.Write(data)
		rs, err := Decode(data)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, rs...)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum == l.last {
		return nil
	}
	res, err := l.Applier.Apply(ctx, all, Options{Owner: l.Owner, Prune: l.Prune})
	if err != nil {
		return err
	}
	l.last = sum
	changed := 0
	for _, c := range res.Changes {
		if c.Action != "unchanged" {
			changed++
			l.Log.Info("manifest applied", "kind", c.Kind, "name", c.Name, "action", c.Action)
		}
	}
	_ = l.Applier.Store.Audit(ctx, "manifest-dir:"+l.Owner, "manifest.apply", l.Dir,
		map[string]any{"files": len(files), "resources": len(all), "changed": changed}, "")
	return nil
}

// Run loads on start and then every interval until ctx ends.
func (l *DirLoader) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := l.Load(ctx); err != nil && ctx.Err() == nil {
			l.Log.Error("manifest directory not applied", "dir", l.Dir, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

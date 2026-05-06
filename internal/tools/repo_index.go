package tools

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/CappyT/beans-preserver/internal/cache"
)

// RepoIndexInput maps Claude's parameters into a directory walk.
type RepoIndexInput struct {
	Path     string `json:"path,omitempty" jsonschema:"directory to index (default: current working directory)"`
	MaxFiles int    `json:"max_files,omitempty" jsonschema:"hard cap on returned files (default 2000)"`
}

type RepoFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

type RepoIndex struct {
	Root      string         `json:"root"`
	Files     []RepoFile     `json:"files"`
	Stats     map[string]int `json:"stats"`
	Truncated bool           `json:"truncated"`
}

// Directories that almost never carry information Claude needs to "know about" in
// an index (build artifacts, vendored deps, IDE state). Walking into them wastes
// the index budget.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, ".next": true, ".nuxt": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true,
	".idea": true, ".vscode": true, ".gradle": true, ".cache": true,
	"bin": true, "out": true, ".turbo": true, ".terraform": true,
}

// Extension → human-friendly "kind". A file with no match falls through as "other".
var extKind = map[string]string{
	".go": "go", ".rs": "rust", ".py": "python", ".rb": "ruby",
	".js": "js", ".jsx": "js", ".ts": "ts", ".tsx": "ts", ".mjs": "js",
	".java": "java", ".kt": "kotlin", ".swift": "swift",
	".c": "c", ".h": "c-hdr", ".cpp": "cpp", ".cc": "cpp", ".hpp": "cpp-hdr",
	".cs": "csharp", ".php": "php", ".lua": "lua", ".sh": "shell", ".bash": "shell",
	".sql": "sql", ".graphql": "graphql", ".proto": "proto",
	".yml": "yaml", ".yaml": "yaml", ".toml": "toml", ".json": "json",
	".xml": "xml", ".ini": "ini", ".env": "env",
	".md": "docs", ".rst": "docs", ".txt": "text",
	".html": "html", ".css": "css", ".scss": "css", ".vue": "vue",
	".dockerfile": "docker", ".tf": "terraform", ".hcl": "hcl",
}

func classify(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "docker"
	case "makefile":
		return "make"
	case "go.mod", "go.sum":
		return "go-mod"
	case "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml":
		return "npm"
	case "cargo.toml", "cargo.lock":
		return "cargo"
	case "requirements.txt", "pyproject.toml", "poetry.lock":
		return "py-pkg"
	case ".gitignore", ".dockerignore":
		return "ignore"
	}
	if k, ok := extKind[filepath.Ext(base)]; ok {
		return k
	}
	return "other"
}

func (r *Runner) RepoIndex(_ context.Context, in RepoIndexInput) (out *RepoIndex, err error) {
	tStart := time.Now()
	defer func() {
		if r.Cache == nil {
			return
		}
		_ = r.Cache.RecordCall(cache.StatEvent{
			Tool:          "repo_index",
			ServerFetched: true,
			WallMs:        time.Since(tStart).Milliseconds(),
			Failed:        err != nil,
		})
	}()

	root := in.Path
	if root == "" {
		root = "."
	}
	max := in.MaxFiles
	if max <= 0 {
		max = 2000
	}

	out = &RepoIndex{
		Root:  root,
		Files: make([]RepoFile, 0, 256),
		Stats: map[string]int{},
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out.Files) >= max {
			out.Truncated = true
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		k := classify(path)
		out.Files = append(out.Files, RepoFile{Path: rel, Size: info.Size(), Kind: k})
		out.Stats[k]++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil //nolint:nakedret // err is set via the named return
}

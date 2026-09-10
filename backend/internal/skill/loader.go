// Package skill loads wiki-writing skill files for the run harness.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// File is one loaded skill document.
type File struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Loader reads skill files from a root directory.
type Loader struct {
	root string
}

// NewLoader creates a loader for the given skill root.
func NewLoader(root string) *Loader {
	return &Loader{root: root}
}

// Root returns the resolved skill root.
func (l *Loader) Root() string { return l.root }

// Exists reports whether the skill root looks valid.
func (l *Loader) Exists() bool {
	if l == nil || l.root == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(l.root, "SKILL.md"))
	return err == nil && !st.IsDir()
}

// ReadFile reads a relative path under the skill root. Rejects path traversal.
func (l *Loader) ReadFile(rel string) (string, error) {
	clean, err := safeRel(rel)
	if err != nil {
		return "", err
	}
	full := filepath.Join(l.root, clean)
	// ensure still under root
	absRoot, err := filepath.Abs(l.root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes skill root: %s", rel)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read skill %s: %w", clean, err)
	}
	return string(b), nil
}

func safeRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" {
		return "", fmt.Errorf("empty skill path")
	}
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("path traversal rejected: %s", rel)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute skill path rejected: %s", rel)
	}
	return filepath.Clean(rel), nil
}

// MediaFile maps a medium to its media-*.md file name. Empty string means none.
func MediaFile(medium domain.Medium) string {
	switch medium {
	case domain.MediumGame:
		return "media-game.md"
	case domain.MediumMovie, domain.MediumTV, domain.MediumAnime:
		return "media-video.md"
	case domain.MediumManga, domain.MediumNovel, domain.MediumBook:
		return "media-book.md"
	default:
		return ""
	}
}

// FilesForCreateWiki returns SKILL.md + core.md + media-*.md for a medium.
func (l *Loader) FilesForCreateWiki(medium domain.Medium) ([]File, error) {
	names := []string{"SKILL.md", "core.md"}
	if m := MediaFile(medium); m != "" {
		names = append(names, m)
	}
	return l.load(names)
}

// FilesForWriteDoc returns general writing boundaries only (no forced 9-chapter).
// Per DESIGN §9 / SKILL.md: write_doc loads SKILL.md only (boundaries, fact rules).
func (l *Loader) FilesForWriteDoc() ([]File, error) {
	return l.load([]string{"SKILL.md"})
}

// FilesForRewriteSection loads incremental-edit guidance (SKILL + core, no media).
func (l *Loader) FilesForRewriteSection(medium domain.Medium) ([]File, error) {
	names := []string{"SKILL.md", "core.md"}
	if m := MediaFile(medium); m != "" {
		names = append(names, m)
	}
	return l.load(names)
}

// FilesForIntent picks the skill bundle for a run intent.
func (l *Loader) FilesForIntent(intent domain.RunIntent, medium domain.Medium) ([]File, error) {
	switch intent {
	case domain.RunIntentCreateWiki, domain.RunIntentContinueWiki:
		return l.FilesForCreateWiki(medium)
	case domain.RunIntentRewriteSection:
		return l.FilesForRewriteSection(medium)
	case domain.RunIntentWriteDoc:
		return l.FilesForWriteDoc()
	case domain.RunIntentAnswer, domain.RunIntentOrganizeTree, domain.RunIntentSyncLibrary:
		// light: no full skill dump
		return nil, nil
	default:
		return l.FilesForCreateWiki(medium)
	}
}

// Names returns just the file names of the loaded bundle.
func Names(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func (l *Loader) load(names []string) ([]File, error) {
	if !l.Exists() {
		return nil, fmt.Errorf("skill root not found or missing SKILL.md: %s", l.root)
	}
	out := make([]File, 0, len(names))
	for _, n := range names {
		content, err := l.ReadFile(n)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: n, Path: n, Content: content})
	}
	return out, nil
}

// ResolveRoot picks the best skill root from candidates that contain SKILL.md.
func ResolveRoot(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		l := NewLoader(c)
		if l.Exists() {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

// DefaultCandidates returns common locations for the wiki-writing skill.
func DefaultCandidates() []string {
	out := []string{}
	if v := os.Getenv("WIKIATLAS_SKILL_ROOT"); v != "" {
		out = append(out, v)
	}
	return append(out,
		"/root/WikiAltas/skills/wiki-writing",
		"skills/wiki-writing",
		filepath.Join("..", "skills", "wiki-writing"),
		filepath.Join("..", "..", "skills", "wiki-writing"),
		".claude/skill/wiki-writing",
	)
}

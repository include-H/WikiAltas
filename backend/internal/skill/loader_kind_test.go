package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
)

func writeKindFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"SKILL.md":      "# Skill\n",
		"core.md":       "# Core\n",
		"media-game.md": "# Game\n",
		"readme.md":     "# Readme\n合集规则\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// series/universe 节点要补注入 readme.md（系列主文/合集规则），否则写合集时看不到规则。
func TestFilesForIntentKindAddsReadmeForSeries(t *testing.T) {
	l := skill.NewLoader(writeKindFixture(t))

	for _, kind := range []domain.WorkKind{domain.WorkKindSeries, domain.WorkKindUniverse} {
		files, err := l.FilesForIntentKind(domain.RunIntentCreateWiki, domain.MediumGame, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		names := skill.Names(files)
		if !strings.Contains(strings.Join(names, ","), "readme.md") {
			t.Fatalf("%s → %v，缺 readme.md", kind, names)
		}
	}
}

func TestFilesForIntentKindLeavesWorkBundleAlone(t *testing.T) {
	l := skill.NewLoader(writeKindFixture(t))
	files, err := l.FilesForIntentKind(domain.RunIntentCreateWiki, domain.MediumGame, domain.WorkKindWork)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(skill.Names(files), ","), "readme.md") {
		t.Fatalf("work 节点不该被补 readme.md：%v", skill.Names(files))
	}
}

func TestFilesForIntentKindNoReadmeWhenMissing(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"SKILL.md": "s", "core.md": "c", "media-game.md": "m"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l := skill.NewLoader(root)
	files, err := l.FilesForIntentKind(domain.RunIntentCreateWiki, domain.MediumGame, domain.WorkKindSeries)
	if err != nil {
		t.Fatalf("readme 缺失不应报错：%v", err)
	}
	if len(files) != 3 {
		t.Fatalf("主 bundle 应保留 3 件，got %v", skill.Names(files))
	}
}

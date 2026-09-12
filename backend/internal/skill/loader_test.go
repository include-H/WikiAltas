package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
)

func writeSkillFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"SKILL.md":       "---\nname: wiki-writing\n---\n# Skill\n通用边界\n",
		"core.md":        "# Core\n9 章骨架\n",
		"media-game.md":  "# Game\n玩法章名\n",
		"media-video.md": "# Video\n影视章名\n",
		"media-book.md":  "# Book\n图书章名\n",
		"frontmatter.md": "# FM\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFilesForCreateWikiGame(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	files, err := l.FilesForCreateWiki(domain.MediumGame)
	if err != nil {
		t.Fatal(err)
	}
	names := skill.Names(files)
	want := []string{"SKILL.md", "core.md", "media-game.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if !strings.Contains(files[2].Content, "玩法章名") {
		t.Fatalf("media-game content missing: %q", files[2].Content)
	}
}

func TestFilesForCreateWikiVideoMedia(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	for _, med := range []domain.Medium{domain.MediumMovie, domain.MediumTV} {
		files, err := l.FilesForCreateWiki(med)
		if err != nil {
			t.Fatalf("%s: %v", med, err)
		}
		if names := skill.Names(files); names[2] != "media-video.md" {
			t.Fatalf("%s → %v", med, names)
		}
	}
}

func TestFilesForCreateWikiBookMedia(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	for _, med := range []domain.Medium{domain.MediumManga, domain.MediumBook} {
		files, err := l.FilesForCreateWiki(med)
		if err != nil {
			t.Fatalf("%s: %v", med, err)
		}
		if names := skill.Names(files); names[2] != "media-book.md" {
			t.Fatalf("%s → %v", med, names)
		}
	}
}

func TestFilesForWriteDocNoMedia(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	files, err := l.FilesForWriteDoc()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "SKILL.md" {
		t.Fatalf("write_doc files = %v", skill.Names(files))
	}
}

func TestFilesForAnswerEmpty(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	files, err := l.FilesForIntent(domain.RunIntentAnswer, domain.MediumGame)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("answer should not preload skill files, got %v", skill.Names(files))
	}
}

func TestPathTraversalRejected(t *testing.T) {
	root := writeSkillFixture(t)
	l := skill.NewLoader(root)
	if _, err := l.ReadFile("../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := l.ReadFile("/etc/passwd"); err == nil {
		t.Fatal("expected absolute path rejection")
	}
}

func TestMissingRoot(t *testing.T) {
	l := skill.NewLoader(filepath.Join(t.TempDir(), "nope"))
	if l.Exists() {
		t.Fatal("should not exist")
	}
	if _, err := l.FilesForCreateWiki(domain.MediumGame); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestMediaFile(t *testing.T) {
	cases := map[domain.Medium]string{
		domain.MediumGame:  "media-game.md",
		domain.MediumMovie: "media-video.md",
		domain.MediumBook:  "media-book.md",
		domain.MediumOther: "",
	}
	for med, want := range cases {
		if got := skill.MediaFile(med); got != want {
			t.Fatalf("MediaFile(%s)=%q want %q", med, got, want)
		}
	}
}

func TestResolveRootFromRepo(t *testing.T) {
	// may find the real skill tree when running inside the repo
	root := skill.ResolveRoot(skill.DefaultCandidates()...)
	if root == "" {
		t.Skip("no skill root found in this environment")
	}
	if !skill.NewLoader(root).Exists() {
		t.Fatalf("resolved root invalid: %s", root)
	}
}

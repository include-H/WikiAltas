package run

import (
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
)

// 复刻真实 core.md 的体量（7459 runes / 18071 bytes）。旧实现按字节判超限、再按 rune 切片：
// 截断越界后补出 NUL，且总预算按字节计被一把撑爆，media-*.md 整个被丢弃。
// 本测试锁死：整套 skill 都要在，且系统提示里不能出现 NUL。
func TestBuildSystemPromptInjectsFullSkillBundle(t *testing.T) {
	const coreRunes = 7459
	files := []skill.File{
		{Name: "SKILL.md", Content: "# skill\nSKILL_MARKER\n"},
		{Name: "core.md", Content: strings.Repeat("龘", coreRunes)},
		{Name: "media-game.md", Content: "# game\nGAME_MEDIA_MARKER\n"},
	}
	sys := buildSystemPromptFor(defaultContextWindow, "edit", domain.RunIntentCreateWiki, domain.MediumGame, files)

	if !strings.Contains(sys, "SKILL_MARKER") {
		t.Fatal("SKILL.md 丢失")
	}
	if !strings.Contains(sys, "GAME_MEDIA_MARKER") {
		t.Fatal("media-*.md 被丢弃（介质级细则从未进过提示）")
	}
	if strings.ContainsRune(sys, '\x00') {
		t.Fatal("系统提示里出现 NUL 字符")
	}
	if n := strings.Count(sys, "龘"); n != coreRunes {
		t.Fatalf("core 保留 %d rune，want %d（未超单文件上限，不应截断）", n, coreRunes)
	}
}

// 超长文件按 rune 截断，且不产生 NUL。
func TestBuildSystemPromptTruncatesByRunesWithoutNul(t *testing.T) {
	const want = 9000 // maxSkillFileRunes
	files := []skill.File{{Name: "core.md", Content: strings.Repeat("龘", 12000)}}
	sys := buildSystemPromptFor(defaultContextWindow, "edit", domain.RunIntentCreateWiki, domain.MediumGame, files)
	if strings.ContainsRune(sys, '\x00') {
		t.Fatal("截断后出现 NUL 字符")
	}
	if n := strings.Count(sys, "龘"); n != want {
		t.Fatalf("截断到 %d rune，want %d", n, want)
	}
}

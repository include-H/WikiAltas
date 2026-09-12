package run

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
)

// 提示词是模型可见文本，"改了一句"必须看得见。
// 照 dsh 的做法把它用 golden 文件钉住：快照红了说明提示词变了，
// 要么是有意改（用 UPDATE_GOLDEN=1 go test ./internal/run/ 重新生成），要么是回归。
func TestSystemPromptGolden(t *testing.T) {
	cases := []struct {
		name      string
		window    int
		docMode   string
		intent    domain.RunIntent
		medium    domain.Medium
		withSkill bool
	}{
		{"create_wiki_edit", 262144, "edit", domain.RunIntentCreateWiki, domain.MediumGame, true},
		{"continue_wiki_edit", 262144, "edit", domain.RunIntentContinueWiki, domain.MediumBook, false},
		{"answer_read", 262144, "read", domain.RunIntentAnswer, "", false},
		{"rewrite_revision", 131072, "revision", domain.RunIntentRewriteSection, domain.MediumGame, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var files []skill.File
			if c.withSkill {
				files = []skill.File{{Name: "SKILL.md", Content: "# skill\n<SAMPLE SKILL BODY>\n"}}
			}
			got := buildSystemPromptFor(c.window, c.docMode, c.intent, c.medium, files)
			path := filepath.Join("testdata", "system_prompt_"+c.name+".golden.md")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("缺 golden 文件（用 UPDATE_GOLDEN=1 go test ./internal/run/ 生成）: %v", err)
			}
			if got != string(want) {
				t.Fatalf("系统提示变了。有意改动就用 UPDATE_GOLDEN=1 重新生成，否则这是回归。\n--- got ---\n%s", got)
			}
		})
	}
}

// 段跟着工具面走：缺席的工具，它的规矩就不该出现。
// 这条直接修掉"闲聊意图还读到『最后 write_content 写入正文并结束』"。
func TestSystemPromptFollowsToolSurface(t *testing.T) {
	readOnly := buildSystemPromptFor(262144, "read", domain.RunIntentAnswer, "", nil)
	for _, banned := range []string{"write_content", "patch_section", "todo_write", "「修订："} {
		if strings.Contains(readOnly, banned) {
			t.Fatalf("只读问答意图不该读到 %q：\n%s", banned, readOnly)
		}
	}
	if !strings.Contains(readOnly, "read-only") && !strings.Contains(readOnly, "No write tool") {
		t.Fatalf("只读意图应说清内容只读：\n%s", readOnly)
	}

	building := buildSystemPromptFor(262144, "edit", domain.RunIntentCreateWiki, domain.MediumGame, nil)
	if !strings.Contains(building, "9-chapter skeleton") {
		t.Fatalf("建档意图应说清这一单是建档：\n%s", building)
	}
	// 工具"存在"由**工具面**保证（用法住在工具定义里，不在提示词里）——
	// 一个事实一个 owner。所以这里查工具面，不查提示词文本。
	names := map[string]bool{}
	for _, d := range buildToolDefsFor(domain.RunIntentCreateWiki, "edit") {
		names[d.Function.Name] = true
	}
	for _, want := range []string{"edit", "patch_section", "write_content", "search_web"} {
		if !names[want] {
			t.Fatalf("建档意图的工具面缺 %s：%v", want, names)
		}
	}
	for _, d := range buildToolDefsFor(domain.RunIntentAnswer, "read") {
		if writeToolNames[d.Function.Name] {
			t.Fatalf("只读问答不该带写工具：%s", d.Function.Name)
		}
	}
}

// 数字与状态从常量注入，不手抄：改了 budget 常量，提示词必须跟着变。
func TestSystemPromptInjectsLiveNumbers(t *testing.T) {
	got := buildSystemPromptFor(65536, "edit", domain.RunIntentCreateWiki, domain.MediumGame, nil)
	if !strings.Contains(got, "65536") {
		t.Fatal("窗口大小应注入到提示词里")
	}
	if !strings.Contains(got, "75%") {
		t.Fatal("压缩触发比例应注入")
	}
	if !strings.Contains(got, strconv.Itoa(maxWebSearches)) {
		t.Fatalf("检索上限应注入（%d）", maxWebSearches)
	}
}

// 管理员标识只作**显示**用：让模型被问「我是谁」时知道在给谁做事，
// 但不能被当成权限凭据——所以提示词里必须同时写清这一点。
func TestSystemPromptCarriesDisplayNameOnly(t *testing.T) {
	base := promptState{
		docMode: "edit", intent: domain.RunIntentAnswer,
		tools: map[string]bool{"answer": true}, compactRatio: compactRatio,
	}
	withName := base
	withName.userName = "馆长大人"
	got := renderSystemPrompt(withName)
	if !strings.Contains(got, "馆长大人") {
		t.Fatal("管理员标识应注入提示词")
	}
	if !strings.Contains(got, "not a credential or a permission") {
		t.Fatal("必须写清它只是显示名，不是权限")
	}
	if plain := renderSystemPrompt(base); strings.Contains(plain, "working in the library of") {
		t.Fatal("没设标识时不该出现这一行")
	}
}

// 重复检测的指纹必须**规范化**：JSON 属性顺序不同但语义相同的参数是同一个调用。
// 拿原始串比会漏判（属性顺序一变就当新调用），护栏形同虚设。
func TestCanonicalArgsIgnoresKeyOrder(t *testing.T) {
	a := canonicalArgs(`{"b":2,"a":1}`)
	b := canonicalArgs(`{"a":1,"b":2}`)
	if a != b {
		t.Fatalf("属性顺序不同应得到同一指纹：%q vs %q", a, b)
	}
	// 嵌套也要规范化
	c := canonicalArgs(`{"x":{"q":1,"p":2}}`)
	d := canonicalArgs(`{"x":{"p":2,"q":1}}`)
	if c != d {
		t.Fatalf("嵌套对象也要规范化：%q vs %q", c, d)
	}
	// 数组顺序有语义，不能被规范化掉
	if canonicalArgs(`[1,2]`) == canonicalArgs(`[2,1]`) {
		t.Fatal("数组顺序是语义的一部分，不该被当成同一个调用")
	}
	// 真不一样就是不一样
	if canonicalArgs(`{"a":1}`) == canonicalArgs(`{"a":2}`) {
		t.Fatal("参数不同不该判成同一个调用")
	}
	// 半截 JSON 不能炸：原样返回，至少同名同串还拦得住
	if got := canonicalArgs(`{"a":`); got != `{"a":` {
		t.Fatalf("半截 JSON 应原样返回，得到 %q", got)
	}
}

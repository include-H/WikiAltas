package run

import (
	"regexp"
	"strings"
)

// QualityResult is the outcome of the create_wiki auto-commit gate.
type QualityResult struct {
	OK       bool     `json:"ok"`
	Issues   []string `json:"issues"`
	CharLen  int      `json:"charLen"`
	Sections int      `json:"sections"`
}

var (
	h2Re   = regexp.MustCompile(`(?m)^##\s+`)
	hAny   = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	linkRe = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s+\S`)
)

// minCharsFloor 对齐 skill core.md §3「全篇默认 5,000–11,000 字」的下限。
// 低于它是低质信号；材料不足时 skill 允许写短并在说明行交代，所以这里只打标、不拒稿。
const minCharsFloor = 5000

// CheckWikiQuality validates a create_wiki draft before commit.
//
// 检查项镜像 skill 的硬要求，不另立一套标准（数字与出处见括注）：
//   - ## 章节存在（core.md §2.1）
//   - 一级章 1–9 齐全、参考资料 ≥5（core.md §2.5）
//   - 有且只有一块 :::epigraph（core.md §4）
//   - 篇幅达到 skill 默认区间下限（core.md §3：5,000–11,000 字）
//
// A failing draft is still committed, but summary is marked low-quality
// and status stays draft rather than ready.
func CheckWikiQuality(md string) QualityResult {
	md = strings.TrimSpace(md)
	r := QualityResult{CharLen: len([]rune(md))}

	if md == "" {
		r.Issues = append(r.Issues, "正文为空")
		return r
	}

	locs := h2Re.FindAllStringIndex(md, -1)
	r.Sections = len(locs)
	if r.Sections == 0 {
		r.Issues = append(r.Issues, "缺少 ## 章节标题")
	}

	// empty / stub sections: body between this ## and next heading is too short
	for i, loc := range locs {
		start := loc[1]
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			// also stop at a trailing # heading if any
			if m := hAny.FindStringIndex(md[start:]); m != nil {
				// only if it's a higher-level or same-level after body; ## already handled
			}
		}
		body := strings.TrimSpace(md[start:end])
		// strip subheadings markers for length estimate
		bodyText := hAny.ReplaceAllString(body, "")
		bodyText = strings.TrimSpace(bodyText)
		n := len([]rune(bodyText))
		if n < 15 {
			r.Issues = append(r.Issues, "存在过短/空章节（约 "+itoa(n)+" 字）")
		}
	}

	if r.CharLen < minCharsFloor {
		r.Issues = append(r.Issues, "篇幅不足 skill 下限 "+itoa(minCharsFloor)+" 字（core.md §3，当前 "+itoa(r.CharLen)+"）")
	}

	// 题记：有且只有一块 :::epigraph（core.md §4）
	if n := strings.Count(md, ":::epigraph"); n == 0 {
		r.Issues = append(r.Issues, "缺少题记围栏 :::epigraph")
	} else if n > 1 {
		r.Issues = append(r.Issues, "题记围栏多于 1 个（应只有一块）")
	}

	// 一级章 1–9 必须齐全（core.md §2.1）
	missing := []string{}
	for i := 1; i <= 9; i++ {
		if !strings.Contains(md, "## "+itoa(i)+".") && !strings.Contains(md, "## "+itoa(i)+" ") {
			missing = append(missing, itoa(i))
		}
	}
	if len(missing) > 0 {
		r.Issues = append(r.Issues, "缺少第 "+strings.Join(missing, "、")+" 章")
	}

	// 参考资料：第 9 章需要 ≥5 条实际用过的来源（core.md §3 / §2.5）
	if idx := strings.LastIndex(md, "## 9."); idx >= 0 {
		tail := md[idx:]
		if next := h2Re.FindStringIndex(tail[len("## 9."):]); next != nil {
			tail = tail[:len("## 9.")+next[0]]
		}
		if n := len(linkRe.FindAllString(tail, -1)); n < 5 {
			r.Issues = append(r.Issues, "参考资料不足 5 条（当前 "+itoa(n)+"）")
		}
	}

	// OK only when no issues. Length alone still fails the gate so mock/short
	// drafts are marked low-quality — that matches "mark summary low quality".
	r.OK = len(r.Issues) == 0
	return r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

package tools_test

import (
	"testing"

	"wikiatlas/backend/internal/tools"
)

// ToolSchemas 是模型读到的工具清单，registry 是可执行的那些。
// 两者成员必须一致，否则会出现"提示里有的工具实际没有"，
// 或者反过来：某个工具永远没机会被用（模型不知道它存在）。
func TestToolSchemasMatchRegistry(t *testing.T) {
	reg := tools.NewLibrarianRegistry(tools.LibrarianDeps{})
	inReg := map[string]bool{}
	for _, n := range reg.Names() {
		inReg[n] = true
	}
	inSchema := map[string]bool{}
	for _, s := range tools.ToolSchemas() {
		inSchema[s.Name] = true
	}
	for n := range inSchema {
		if !inReg[n] {
			t.Errorf("ToolSchemas 里有 %s，但 registry 没注册它", n)
		}
	}
	for n := range inReg {
		if !inSchema[n] {
			t.Errorf("registry 注册了 %s，但 ToolSchemas 里没有——模型看不到它", n)
		}
	}
}

// 每条工具描述都要有实质内容：一句话的空壳会让模型靠猜。
// 工具用法住在描述里（一个事实一个 owner），所以描述必须够厚。
func TestToolSchemasHaveSubstantiveDescriptions(t *testing.T) {
	for _, s := range tools.ToolSchemas() {
		if n := len([]rune(s.Desc)); n < 40 {
			t.Errorf("%s 的描述过短（%d 字）：%q", s.Name, n, s.Desc)
		}
	}
}

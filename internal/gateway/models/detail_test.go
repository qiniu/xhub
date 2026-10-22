package models

import "testing"

// TestModelDetailShape 验证正常与空名称的两种SDK元数据契约，参数t为上下文，返回无。
// 前置内存公开名；不得添加部署或凭据字段，测试无外部依赖或持久数据。
func TestModelDetailShape(t *testing.T) {
	for _, name := range []string{"claude-public", ""} {
		for _, anthropic := range []bool{false, true} {
			got := modelDetail(name, anthropic)
			if len(got) != 4 || got["id"] != name {
				t.Fatalf("模型元数据泄漏或名称变化: %+v", got)
			}
			if anthropic && (got["type"] != "model" || got["display_name"] != name || got["created_at"] == nil) {
				t.Fatalf("Anthropic模型元数据错误: %+v", got)
			}
			if !anthropic && got["object"] != "model" {
				t.Fatalf("OpenAI模型元数据错误: %+v", got)
			}
		}
	}
}

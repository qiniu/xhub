package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestCloneModels 验证空值、嵌套容器及整数精度，且非法配置不被复制过程悄悄修正。
// 前置纯内存 YAML/JSON 形状和类型化容器；修改副本不得影响输入，非法值仍令 JSON 校验失败；无需清理。
func TestCloneModels(t *testing.T) {
	for _, input := range [][]ModelEntry{nil, {}, {{}}} {
		if got := CloneModels(input); !reflect.DeepEqual(input, got) {
			t.Fatalf("空配置形状改变: %#v -> %#v", input, got)
		}
	}
	var nilSlice []any
	input := []ModelEntry{{LiteLLMParams: map[string]any{
		"n": json.Number("9007199254740993"), "int": int64(9007199254740993), "nil": nil, "slice": nilSlice,
		"nested": []map[string]any{{"list": []any{map[string]string{"key": "before"}}}},
		"yaml":   map[any]any{"list": []string{"before"}},
		"empty":  []any{},
	}}}
	got := CloneModels(input)
	if !reflect.DeepEqual(input, got) {
		t.Fatal("快照改变具体类型、nil 或数字精度")
	}
	got[0].LiteLLMParams["nested"].([]map[string]any)[0]["list"].([]any)[0].(map[string]string)["key"] = "after"
	got[0].LiteLLMParams["yaml"].(map[any]any)["list"].([]string)[0] = "after"
	if input[0].LiteLLMParams["nested"].([]map[string]any)[0]["list"].([]any)[0].(map[string]string)["key"] != "before" || input[0].LiteLLMParams["yaml"].(map[any]any)["list"].([]string)[0] != "before" {
		t.Fatal("嵌套配置仍共享")
	}
	input[0].LiteLLMParams["invalid"] = make(chan int)
	if _, err := json.Marshal(CloneModels(input)); err == nil {
		t.Fatal("非法配置在复制过程中被丢弃")
	}
}

// BenchmarkCloneModels 测量包含嵌套参数的目录复制成本；参数 b 为基准运行器，结果含分配量；无数据库或外部状态。
func BenchmarkCloneModels(b *testing.B) {
	models := make([]ModelEntry, 100)
	for i := range models {
		models[i] = ModelEntry{ModelName: "public", LiteLLMParams: map[string]any{"model": "wire", "api_base": "http://localhost", "vendor": map[string]any{"values": []any{1, "x"}}}, ModelInfo: map[string]any{"endpoint_types": []string{"chat", "responses"}}}
	}
	b.ReportAllocs()
	for b.Loop() {
		CloneModels(models)
	}
}

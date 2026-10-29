package gateway

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/router"
)

// TestModelsSnapshotIsolation 验证数据面目录与进程配置双向隔离，保留嵌套值和精确数字类型。
// 前置仅内存 Server；修改返回值及模拟管理端锁内更新后，旧快照保持不变，新快照可见更新；无外部数据待清理。
func TestModelsSnapshotIsolation(t *testing.T) {
	s := &Server{Cfg: &config.Config{ModelList: []config.ModelEntry{{ModelName: "old", LiteLLMParams: map[string]any{
		"vendor": map[string]any{"values": []any{json.Number("9007199254740993"), map[string]any{"enabled": true}}},
	}, ModelInfo: map[string]any{"endpoint_types": []string{"chat"}}}}}}
	view := s.Models()
	view[0].ModelInfo["endpoint_types"].([]string)[0] = "responses"
	view[0].LiteLLMParams["vendor"].(map[string]any)["values"].([]any)[1].(map[string]any)["enabled"] = false
	if s.Cfg.ModelList[0].ModelInfo["endpoint_types"].([]string)[0] != "chat" || s.Cfg.ModelList[0].LiteLLMParams["vendor"].(map[string]any)["values"].([]any)[1].(map[string]any)["enabled"] != true {
		t.Fatal("请求修改嵌套配置污染进程目录")
	}
	s.LockModels()
	s.Cfg.ModelList[0].ModelName = "new"
	s.Cfg.ModelList[0].ModelInfo["disabled"] = true
	s.UnlockModels()
	if view[0].ModelName != "old" || view[0].Disabled() || s.Models()[0].ModelName != "new" || !s.Models()[0].Disabled() {
		t.Fatal("热更新污染旧请求或后续请求仍读旧目录")
	}
	if view[0].LiteLLMParams["vendor"].(map[string]any)["values"].([]any)[0] != json.Number("9007199254740993") {
		t.Fatal("快照改变精确数字或类型")
	}
}

// TestModelsSnapshotConcurrentUpdate 验证管理写锁与目录读取使用同一同步边界；race 检测和成对字段断言共同防止混读。
// 前置内存目录，两协程反复更新/读取；WaitGroup 等待结束，无定时等待和外部资源。
func TestModelsSnapshotConcurrentUpdate(t *testing.T) {
	s := &Server{Cfg: &config.Config{ModelList: []config.ModelEntry{{ModelInfo: map[string]any{"a": 0, "b": 0}}}}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 100; n++ {
			s.LockModels()
			s.Cfg.ModelList[0].ModelInfo["a"], s.Cfg.ModelList[0].ModelInfo["b"] = n, n
			s.UnlockModels()
		}
	}()
	for n := 0; n < 100; n++ {
		view := s.Models()
		if view[0].ModelInfo["a"] != view[0].ModelInfo["b"] {
			t.Error("目录读取混入两个管理版本")
		}
	}
	wg.Wait()
}

// TestFindDeploymentSnapshot 验证按运行身份查找只返回独立模型，缺失身份不误匹配；纯内存无需清理。
func TestFindDeploymentSnapshot(t *testing.T) {
	s := &Server{Cfg: &config.Config{ModelList: []config.ModelEntry{{ModelName: "public", LiteLLMParams: map[string]any{"model": "wire", "api_base": "http://localhost"}, ModelInfo: map[string]any{"nested": map[string]any{"flag": true}}}}}}
	found, ok := s.FindDeployment(router.CooldownID(s.Cfg.ModelList[0]))
	if !ok {
		t.Fatal("运行部署未找到")
	}
	found.ModelInfo["nested"].(map[string]any)["flag"] = false
	if s.Cfg.ModelList[0].ModelInfo["nested"].(map[string]any)["flag"] != true {
		t.Fatal("按ID查找仍暴露共享配置")
	}
	if _, ok := s.FindDeployment("missing"); ok {
		t.Fatal("缺失部署误匹配")
	}
}

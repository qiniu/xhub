package suppliers

import (
	"strconv"
	"testing"
)

// TestServiceMaskRoundTrip 验证短到长秘密的详情掩码回传后仍保留原值，尤其五字节只含一个星号的边界。
// 前置内存存储，实际调用创建/详情/更新服务；同时验证普通地址字段更新，测试无外部资源需清理。
func TestServiceMaskRoundTrip(t *testing.T) {
	for _, secret := range []string{"", "a", "abcd", "abcde", "abcdef", "fixture-long-key"} {
		t.Run("length-"+strconv.Itoa(len(secret)), func(t *testing.T) {
			st := &memoryStore{rows: map[string]map[string]any{}}
			s := Service{Store: st, Kind: "credentials"}
			if _, err := s.Create("provider", map[string]any{"credential_values": map[string]any{"api_key": secret, "api_base": "old"}}); err != nil {
				t.Fatal(err)
			}
			read, err := s.Get("provider")
			if err != nil {
				t.Fatal(err)
			}
			values := read["credential_values"].(map[string]any)
			values["api_base"] = "new"
			if _, err = s.Update("provider", map[string]any{"credential_values": values}); err != nil {
				t.Fatal(err)
			}
			stored := st.rows["provider"]["credential_values"].(map[string]any)
			if stored["api_key"] != secret {
				t.Fatalf("长度 %d 的掩码回传覆盖了真实 Key", len(secret))
			}
			if stored["api_base"] != "new" {
				t.Fatal("保留掩码不应阻止普通字段修改")
			}
		})
	}
}

// TestServiceSingleStarReplacement 验证含单个星号的真实新秘密仍可设置，只有与当前存储值的掩码相等才保留旧值。
// 内存服务覆盖已有值/无旧值/旧值非字符串与不同敏感字段；无外部清理，不能把所有单星号输入误判为掩码。
func TestServiceSingleStarReplacement(t *testing.T) {
	for _, key := range []string{"api_key", "access_token", "client_secret"} {
		for _, old := range []any{nil, 42, "abcde", "long-original-key"} {
			st := &memoryStore{rows: map[string]map[string]any{}}
			s := Service{Store: st, Kind: "credentials"}
			if _, err := s.Create("provider", map[string]any{"credential_values": map[string]any{key: old}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Update("provider", map[string]any{"credential_values": map[string]any{key: "xy*z9"}}); err != nil {
				t.Fatal(err)
			}
			if st.rows["provider"]["credential_values"].(map[string]any)[key] != "xy*z9" {
				t.Fatalf("合法单星号新秘密被忽略: %s", key)
			}
		}
	}
}

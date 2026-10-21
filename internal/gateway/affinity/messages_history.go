package affinity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// messagesHistoryRecord 将整轮原生助手内容及其公开投影绑定到创建时的会话和真实连接。
// 生产存于服务端PostgreSQL；客户端得到随机引用而非签名明文，引用不能跨调用方或模型授权使用。
type messagesHistoryRecord struct {
	Version int                        `json:"version"`
	Session string                     `json:"session"`
	Target  dpcontract.RouteTarget     `json:"target"`
	Replay  llmdialogue.MessagesReplay `json:"replay"`
}

// messagesHistoryKey 为随机引用构造身份/别名/入口隔离键，供保存与恢复共用。
// 参数均来自可信计划，引用来自客户端但不参与权限授予；不访问存储或暴露原字段。
func messagesHistoryKey(plan dpcontract.RoutePlan, ref string) string {
	return strings.Replace(responsePinKey(plan.Alias, plan.Caller, ref, plan.Endpoint), "session_route:v1:response:", "session_route:v1:messages:", 1)
}

// messagesHistoryRefs 收集 Responses 输入中的网关引用并验证版本形状，供选路恢复使用。
// 仅识别保留前缀，未知厂商密文仍遵循既有 portable 策略；非法网关引用不能被清理成普通历史。
// 返回去重后的引用或字段错误，最多256个；没有外部副作用。
func messagesHistoryRefs(items any) ([]string, error) {
	values, _ := items.([]any)
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		item, _ := value.(map[string]any)
		ref, _ := item["encrypted_content"].(string)
		if !strings.HasPrefix(ref, "xhub_messages_") {
			continue
		}
		suffix := strings.TrimPrefix(ref, llmdialogue.MessagesHistoryPrefix)
		decoded, err := hex.DecodeString(suffix)
		if item["type"] != "reasoning" || !strings.HasPrefix(ref, llmdialogue.MessagesHistoryPrefix) || err != nil || len(decoded) != 32 || suffix != strings.ToLower(suffix) {
			return nil, fmt.Errorf("invalid Messages history reference")
		}
		if !seen[ref] {
			out = append(out, ref)
			seen[ref] = true
		}
		if len(out) > 256 {
			return nil, fmt.Errorf("too many Messages history references")
		}
	}
	return out, nil
}

// prepareMessagesHistory 恢复完整历史和父响应历史中的原生助手引用，并安装本轮发布回调。
// 参数plan必须已有可信Caller与会话；body只读。失败返回统一不可用错误，执行器必须零外发。
// 持久存储读取失败禁止降级猜测；每个引用必须属于相同真实连接，不能让普通请求重新绑定旧签名。
func (s *State) prepareMessagesHistory(plan *dpcontract.RoutePlan, body map[string]any) error {
	if plan.Endpoint != "responses" || plan.Caller == "" {
		return nil
	}
	items, _ := body["input"].([]any)
	all := append([]any(nil), items...)
	for _, turn := range plan.History {
		if turn.NativeProtocol == "openai-responses" && turn.Native != nil {
			all = append(all, turn.Native)
		}
	}
	refs, err := messagesHistoryRefs(all)
	if err != nil {
		return err
	}
	plan.MessagesHistory = map[string]llmdialogue.MessagesReplay{}
	for _, ref := range refs {
		raw, _, err := s.cacheRead(messagesHistoryKey(*plan, ref), false)
		var record messagesHistoryRecord
		if err != nil || len(raw) > 4<<20 || json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 || record.Session != plan.SessionID || record.Target.DeploymentID == "" || record.Target.ConnectionID == "" || record.Replay.Message == nil {
			return fmt.Errorf("Messages history is unavailable or expired; start a new conversation")
		}
		if plan.Required && (plan.Target.DeploymentID != record.Target.DeploymentID || plan.Target.ConnectionID != record.Target.ConnectionID) {
			return fmt.Errorf("Messages history belongs to another connection")
		}
		plan.Target, plan.Pinned, plan.Required = record.Target, record.Target.DeploymentID, true
		plan.MessagesHistory[ref] = record.Replay
	}
	scope := *plan
	plan.CommitMessagesHistory = func(target dpcontract.RouteTarget, responseID string, history []llmdialogue.Turn) error {
		// 回调只捕获可信身份范围；实际目标及公开历史由执行器在完整解析后提供。
		if responseID == "" || len(history) == 0 {
			return fmt.Errorf("Messages response history is incomplete")
		}
		saved := scope
		// Save 在 PlanRoute 后半段安装；调用时读取完整计划，保留成功后的偏好更新。
		saved.Save = plan.Save
		saved.Target, saved.History = target, history
		return s.commitRoute(saved, target.DeploymentID, responseID)
	}
	plan.PrepareMessages = func(target dpcontract.RouteTarget, result llmdialogue.DialogueResult) (llmdialogue.DialogueResult, error) {
		// 只有签名/隐藏块需要不可迁移状态；普通公开推理和文字维持既有可迁移行为。
		if result.Source != "anthropic-messages" || !opaqueMessagesResult(result) {
			return result, nil
		}
		if target.DeploymentID == "" || target.ConnectionID == "" {
			return result, fmt.Errorf("Messages history requires a bound connection")
		}
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return result, fmt.Errorf("Messages history reference unavailable")
		}
		ref := llmdialogue.MessagesHistoryPrefix + hex.EncodeToString(nonce[:])
		prepared, err := llmdialogue.ResponsesWithMessagesHistory(result, ref)
		if err != nil {
			return result, err
		}
		output, _ := prepared.Native["output"].([]any)
		if len(output) == 0 {
			return result, fmt.Errorf("Messages history projection unavailable")
		}
		record := messagesHistoryRecord{Version: 1, Session: scope.SessionID, Target: target, Replay: llmdialogue.MessagesReplay{Message: result.Native, Output: output[1:]}}
		raw, err := json.Marshal(record)
		if err != nil || len(raw) > 4<<20 {
			return result, fmt.Errorf("Messages history exceeds supported size")
		}
		if err = s.cacheWrite(messagesHistoryKey(scope, ref), string(raw)); err != nil {
			return result, fmt.Errorf("Messages history could not be saved")
		}
		return prepared, nil
	}
	return nil
}

// opaqueMessagesResult 判断已校验的Messages回复是否包含签名或不可见推理；仅用于决定保存引用。
// 参数来自原生解析器，不接受客户端作为可信结果；无存储副作用，公开无签名thinking不固定原连接。
func opaqueMessagesResult(result llmdialogue.DialogueResult) bool {
	blocks, _ := result.Native["content"].([]any)
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		if block["type"] == "redacted_thinking" {
			return true
		}
		if block["type"] == "thinking" {
			if sig, ok := block["signature"].(string); ok && sig != "" {
				return true
			}
		}
	}
	return false
}

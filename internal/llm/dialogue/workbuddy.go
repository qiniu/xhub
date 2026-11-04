package dialogue

import (
	"fmt"
	"math"
)

// projectWorkBuddyMessageTracking 校验并移除 WorkBuddy Chat 消息中的客户端跟踪字段。
// 参数是解析器创建的消息副本；返回类型错误，供 Chat 到其他协议的共享投影使用。
// 同协议原始正文不受影响；仅识别实测 cli 标记和已知跟踪字段，未知能力仍由解析器拒绝。
func projectWorkBuddyMessageTracking(message map[string]any) error {
	agent, exists := message["agent"]
	if !exists || agent != "cli" {
		return nil
	}
	if role := dialogueString(message["role"]); role != "user" && role != "assistant" {
		return fmt.Errorf("invalid WorkBuddy message role")
	}
	for _, key := range []string{"conversationRequestId", "messageId", "model", "requestModelId", "requestModelName", "traceId"} {
		if value, ok := message[key]; ok {
			if _, valid := value.(string); !valid {
				return fmt.Errorf("invalid WorkBuddy %s", key)
			}
			delete(message, key)
		}
	}
	if value, ok := message["startsNewUserRequest"]; ok {
		if _, valid := value.(bool); !valid {
			return fmt.Errorf("invalid WorkBuddy startsNewUserRequest")
		}
		delete(message, "startsNewUserRequest")
	}
	if value, ok := message["stepSeq"]; ok {
		number, valid := dialogueNumber(value)
		if !valid || number < 0 || math.Trunc(number) != number {
			return fmt.Errorf("invalid WorkBuddy stepSeq")
		}
		delete(message, "stepSeq")
	}
	for _, key := range []string{"usage", "rawUsage"} {
		if value, ok := message[key]; ok {
			if _, valid := value.(map[string]any); !valid {
				return fmt.Errorf("invalid WorkBuddy %s", key)
			}
			delete(message, key)
		}
	}
	delete(message, "agent")
	return nil
}

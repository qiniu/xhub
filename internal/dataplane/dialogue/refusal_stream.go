package dialogue

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// finishRefusal 将已收到完整 Messages 终态的拒绝说明转换成 OpenAI 标准拒绝。
// 无参数，使用本请求累计状态；finish 调用，返回无法表达、部分输出或 HTTP 写入错误。
// 不伪造说明；缺说明或已有正文/工具/推理时明确失败，不提交成功历史。成功时保留已测用量，终态前调用完成回调。
func (s *dialogueStream) finishRefusal() error {
	if s.messagesError != nil {
		return s.messagesError
	}
	if !s.messagesVerified.Refused {
		return fmt.Errorf("invalid upstream Messages refusal lifecycle")
	}
	s.usage = llmdialogue.NormalizeDialogueUsage(s.usage)
	result := s.messagesVerified
	raw, err := llmdialogue.EncodeDialogueResult(result, s.protocol, s.model)
	if err != nil {
		return err
	}
	if err = s.start(); err != nil {
		return err
	}
	if s.protocol == "openai-chat" {
		if err = s.chat(map[string]any{"refusal": s.refusal}, nil); err != nil {
			return err
		}
		if s.beforeComplete != nil {
			s.beforeComplete(result)
		}
		if err = s.chat(map[string]any{}, "stop"); err != nil {
			return err
		}
		if err = s.emit("", map[string]any{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{}, "usage": s.usage}); err != nil {
			return err
		}
		s.capture.WriteString("data: [DONE]\n\n")
		_, err = io.WriteString(s.writer, "data: [DONE]\n\n")
		if f, ok := s.writer.(http.Flusher); ok {
			f.Flush()
		}
		return err
	}
	var response map[string]any
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	id := llmdialogue.ResponseItemID("msg", s.responseItemSeed)
	part := map[string]any{"type": "refusal", "refusal": s.refusal}
	item := map[string]any{"id": id, "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	response["output"] = []any{item}
	for _, event := range []map[string]any{
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
		{"type": "response.content_part.added", "item_id": id, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "refusal", "refusal": ""}},
		{"type": "response.refusal.delta", "item_id": id, "output_index": 0, "content_index": 0, "delta": s.refusal},
		{"type": "response.refusal.done", "item_id": id, "output_index": 0, "content_index": 0, "refusal": s.refusal},
		{"type": "response.content_part.done", "item_id": id, "output_index": 0, "content_index": 0, "part": part},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
	} {
		if err = s.emit(streamString(event["type"]), event); err != nil {
			return err
		}
	}
	if s.beforeComplete != nil {
		s.beforeComplete(result)
	}
	return s.emit("response.completed", map[string]any{"type": "response.completed", "response": response})
}

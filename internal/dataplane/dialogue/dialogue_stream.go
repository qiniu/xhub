// 本文件把登记的上游 SSE 解码为文本/工具/用量，再编码到客户端协议；必须看到可结束终态才可提交完成。
// Responses 的 incomplete 允许结束，但保留生成未完整完成的原因；失败或缺失终态不提交完成。
// 调用关系与边界：统一对话请求构造与流协议转换；具体参数、返回及副作用见各方法说明。
package dialogue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"github.com/sunqirui1987/xhub/internal/util"
)

// dialogueStream 是单次协议流转换的状态机，由 PipeDialogue 创建并驱动。
// 累积统一文本、工具调用和用量，再编码为客户端所需 SSE；上游索引、输出内容块和 Chat 工具索引分别管理。
// started 表示开始事件已处理，wrote 表示实际写出过字节，completed 表示看到了可结束终态，三者不能互相替代。
// Responses 的 incomplete 也可结束，但会保留 length 原因，不伪装成完整生成。
// 只在当前请求内串行使用；完成前验证工具参数，Responses 终态前提交已验证的续接历史。
type dialogueStream struct {
	// prepareResult 在完成校验后、任何 done 事件前保存不透明引用；错误阻止成功终态。
	prepareResult func(llmdialogue.DialogueResult) (llmdialogue.DialogueResult, error)
	// messagesResponses 标识受控原生 Messages 到 Responses 的签名桥接，公开流只发布摘要与缓存引用。
	messagesResponses bool
	// messagesReasoning 表示已预留首个引用项；无推理普通回复不分配索引，不产生空项。
	messagesReasoning bool
	// observeOnly 只复用增量校验和用量累计，原生输出由 PipeNativeDialogue 保留完整事件。
	observeOnly bool
	// nativeResponses 仅用于灰度原生 SSE 终态观测，不改变跨协议转换。
	nativeResponses bool
	// reasoning 保存公开推理文本；索引用于 Responses 的推理输出生命周期。
	reasoning        string
	reasoningStarted bool
	reasoningBlock   int
	// Chat 的 reasoning_items 只在 Responses 终态校验后发布，避免泄漏半截密文或伪造成功。
	chatReasoningItems []any
	// Responses -> Messages 的签名仅在推理项完成时可靠；在此之前保留正文顺序，其他协议不使用这些状态。
	responsesMessages        bool
	responsePrelude          bool
	responseReasoningPending bool
	pendingResponseText      string
	responseReasoningBlocks  map[string]string
	// Messages 同时只开放一个内容块；工具参数完成校验后串行发送，避免并行片段写入已关闭块。
	messageBlockKind  string
	messageBlockIndex int
	// 客户端 HTTP 写入器，写错误沿转换链返回。
	writer http.ResponseWriter
	// 转换后 SSE 的日志捕获缓冲，写客户端前记录，因此可能包含未实际送达的尾片段。
	capture bytes.Buffer
	// 客户端协议、公开模型、响应 ID、累计文本与归一结束原因；均为本请求私有状态。
	protocol, model, id, text, stop string
	// refused/refusal 保存 Messages 的拒绝事实和真实说明；只在完整终态后输出标准 refusal 字段。
	refused bool
	refusal string
	// Messages 拒绝终态复用原生生命周期校验；不能只凭一个 stop_reason 将缺开始、未闭合块或冲突终态发布成成功。
	messagesVerifier nativeMessagesStream
	messagesVerified llmdialogue.DialogueResult
	messagesError    error
	// responseItemSeed 在首次输出时固定；上游迟到或重复的响应 ID 不得改变已发布的 item_id。
	responseItemSeed string
	// 上游工具索引到累积调用的映射，逐帧拼接名称和 JSON 参数。
	calls map[int]*llmdialogue.Turn
	// 上游工具索引到稳定输出内容块索引的映射，避免与文本块碰撞。
	blocks map[int]int
	// 本次上游报告的统计，分帧覆盖字段，不按每帧重复求和。
	usage map[string]any
	// 开始事件已处理、实际写出过字节、看到可结束终态三个独立事实；wrote 后不可重试。
	started, wrote, completed bool
	// 下一个可分配的输出块索引，文本和工具共用。
	nextBlock int
	// 文本块的稳定索引，-1 表示尚未创建。
	textBlock int
	// Responses 事件单调递增序号，emit 给事件对象补入。
	sequence int
	// 稀疏上游工具索引到 Chat 连续工具索引的映射。
	toolIndices map[int]int
	// 可选完成回调，参数为已验证 DialogueResult，无返回；Responses 终态发出前提交历史。
	// 终态校验前失败不调用；回调之后若客户端写入失败，已提交历史不回滚。
	beforeComplete func(llmdialogue.DialogueResult)
	// namespace/custom 工具必须在参数完整时还原，避免把半截 JSON 包装泄露为 custom 输入。
	toolBindings map[string]llmdialogue.ToolBinding
}

// streamMap 读取流事件中的动态对象字段。
// 参数 v 为任意 JSON 值；返回 map 或 nil；consume 等解码辅助方法调用。
// 非对象不报错，后续协议分支负责语义校验；不复制对象、不修改输入。
func streamMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// streamString 读取流事件中的动态字符串字段。
// 参数 v 为任意值；返回字符串，类型不匹配或 nil 返回空串；流事件解码调用。
// 不将数字或对象隐式转成字符串，不产生外部副作用；必需字段的缺失由完成校验处理。
func streamString(v any) string { s, _ := v.(string); return s }

// streamIndex 读取普通 JSON 解码得到的流事件索引。
// 参数 v 应为 float64；返回截断整数，其他类型返回零；consume 用于定位工具或输出项。
// 不做索引范围/唯一性验证，也不支持所有数字类型；只是当前解码方式的局部辅助函数。
func streamIndex(v any) int { n, _ := v.(float64); return int(n) }

// emit 将一条调用方协议事件编码成完整 SSE 帧，并立即尝试刷新客户端。
// 参数 event 为可空事件名，data 为 JSON 数据；返回编码/写入错误，由全部输出方法调用。
// Responses 为对象原地加入递增 sequence_number；先捕获拟发送帧，再 Write，实际 n>0 才置 wrote。
// capture 可能包含未完全写成功的帧，因此不能作为客户端完整接收的证明；写出后执行器必须禁止换部署。
// 副作用为状态序号、捕获缓冲和 HTTP 输出；刷新只在 writer 支持 http.Flusher 时执行。
func (s *dialogueStream) emit(event string, data any) error {
	if s.observeOnly {
		return nil
	}
	if s.protocol == "openai-responses" {
		if m, ok := data.(map[string]any); ok {
			// 跨协议桥接与原生统一入口使用相同的可迁移推理策略，不能向客户端泄漏桥接来源 ID。
			if item, ok := m["item"].(map[string]any); ok {
				if !(s.messagesResponses && item["type"] == "reasoning") {
					m["item"] = llmdialogue.PortableReasoningItems([]any{item}).([]any)[0]
				}
			}
			if response, ok := m["response"].(map[string]any); ok {
				public := llmdialogue.PortableResponsesObject(response, "output")
				if s.messagesResponses {
					for i, value := range streamList(public["output"]) {
						item := streamMap(value)
						if item["type"] == "reasoning" {
							item["id"] = streamMap(streamList(response["output"])[i])["id"]
						}
					}
				}
				m["response"] = public
			}
			m["sequence_number"] = s.sequence
			s.sequence++
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var b strings.Builder
	if event != "" {
		fmt.Fprintf(&b, "event: %s\n", event)
	}
	fmt.Fprintf(&b, "data: %s\n\n", raw)
	chunk := []byte(b.String())
	s.capture.Write(chunk)
	n, err := s.writer.Write(chunk)
	if n > 0 {
		s.wrote = true
	}
	if f, ok := s.writer.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

// start 按调用方协议发送首次生命周期事件，保证同一请求只开始一次。
// 无参数；返回写入/不支持协议错误；textDelta、toolDelta 与完成处理在首次输出前调用。
// Chat 输出 assistant 角色，Responses 输出 created，Messages 输出 message_start；Google 不需要独立开始帧。
// 修改 started 并固定合成文本项的 ID 种子，可能写 HTTP；重复调用不重置种子、用量或块索引。
func (s *dialogueStream) start() error {
	if s.started {
		return nil
	}
	s.started = true
	s.responseItemSeed = s.id
	switch s.protocol {
	case "gemini", "vertex":
		return nil
	case "openai-chat":
		return s.chat(map[string]any{"role": "assistant"}, nil)
	case "openai-responses":
		return s.emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": s.id, "object": "response", "model": s.model, "status": "in_progress", "store": false, "output": []any{}}})
	case "anthropic-messages":
		return s.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": s.id, "type": "message", "role": "assistant", "model": s.model, "content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}}})
	}
	return fmt.Errorf("unsupported stream protocol")
}

// chat 构造调用方 Chat 协议的单个增量 chunk。
// 参数 delta 为角色/文本/工具增量，finish 为可空结束原因；返回 emit 的写入错误。
// Chat 输出分支调用；使用本轮响应 ID、公开模型和首个 choice，当前时间作为 created。
// 只负责封装，不累计 token 或校验终态；通过 emit 改变捕获与写出状态。
func (s *dialogueStream) chat(delta map[string]any, finish any) error {
	return s.emit("", map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
}

// reasoningDelta 累积公开推理文本并按目标协议输出，参数为增量字符串，返回协议或写入错误。
// consume 调用；同协议观察模式只累计，Responses 输出独立 reasoning 项，Messages 输出无签名 thinking 内容块。
// 无外部调用，会修改流状态和写出字节；不得把密文传入本方法。
func (s *dialogueStream) reasoningDelta(text string) error {
	if text == "" {
		return nil
	}
	s.reasoning += text
	if s.observeOnly {
		return nil
	}
	if s.protocol != "openai-chat" && s.protocol != "openai-responses" && s.protocol != "anthropic-messages" {
		return fmt.Errorf("reasoning stream requires OpenAI protocol")
	}
	if err := s.start(); err != nil {
		return err
	}
	if s.protocol == "openai-chat" {
		return s.chat(map[string]any{"reasoning_content": text}, nil)
	}
	if s.protocol == "anthropic-messages" {
		return s.messageDelta(map[string]any{"type": "thinking", "thinking": "", "signature": ""}, map[string]any{"type": "thinking_delta", "thinking": text})
	}
	if !s.reasoningStarted {
		s.reasoningStarted = true
		s.reasoningBlock = s.nextBlock
		s.nextBlock++
		item := map[string]any{"id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "type": "reasoning", "summary": []any{}}
		if err := s.emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": s.reasoningBlock, "item": item}); err != nil {
			return err
		}
		if err := s.emit("response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "item_id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "output_index": s.reasoningBlock, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}); err != nil {
			return err
		}
	}
	return s.emit("response.reasoning_summary_text.delta", map[string]any{"type": "response.reasoning_summary_text.delta", "item_id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "output_index": s.reasoningBlock, "summary_index": 0, "delta": text})
}

// textDelta 累积并按调用方协议输出一段文本增量。
// 参数 text 为新增文本；返回写入错误，空文本直接成功；consume 解码文本事件后调用。
// 确保开始事件已发出，将文本累积到 s.text；Responses/Messages 首次文本创建固定内容块，再发送增量。
// Google/Chat 直接输出相应文本 chunk；输出块与工具共用分配器以保持最终 output_index 对应。
// 会修改请求状态并写 HTTP，不把空文本当作有效生成或完成证明。
func (s *dialogueStream) textDelta(text string) error {
	if text == "" {
		return nil
	}
	if err := s.start(); err != nil {
		return err
	}
	s.text += text
	switch s.protocol {
	case "gemini", "vertex":
		return s.emit("", map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}}})
	case "openai-chat":
		return s.chat(map[string]any{"content": text}, nil)
	case "openai-responses":
		if s.textBlock < 0 {
			s.textBlock = s.nextBlock
			s.nextBlock++
			if err := s.emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": s.textBlock, "item": map[string]any{"id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}); err != nil {
				return err
			}
			if err := s.emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "output_index": s.textBlock, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
				return err
			}
		}
		return s.emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "output_index": s.textBlock, "content_index": 0, "delta": text})
	case "anthropic-messages":
		return s.messageDelta(map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text_delta", "text": text})
	}
	return nil
}

// closeMessageBlock 关闭当前 Messages 内容块；无参数，返回 HTTP 写入错误；阶段切换与 finish 调用。
// 仅在成功输出 stop 后清空活动状态，不重复关闭，也不生成 message_stop；不完整上游流不会调用正常完成链。
func (s *dialogueStream) closeMessageBlock() error {
	if s.messageBlockKind == "" {
		return nil
	}
	if err := s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.messageBlockIndex}); err != nil {
		return err
	}
	s.messageBlockKind = ""
	return nil
}

// messageDelta 将一个公开内容增量编码为顺序 Messages 块；参数为起始块和增量对象，返回写入错误。
// 推理、正文及完成后的工具调用使用；类型切换先关闭旧块，再分配递增索引，避免 Claude Code 收到重叠或错类型增量。
// 只影响本请求 SSE，不生成签名、不修改输入对象；工具调用方在不同工具之间显式关闭块。
func (s *dialogueStream) messageDelta(block, delta map[string]any) error {
	kind := streamString(block["type"])
	if s.messageBlockKind != kind {
		if err := s.closeMessageBlock(); err != nil {
			return err
		}
		s.messageBlockKind, s.messageBlockIndex = kind, s.nextBlock
		s.nextBlock++
		if err := s.emit("content_block_start", map[string]any{"type": "content_block_start", "index": s.messageBlockIndex, "content_block": block}); err != nil {
			return err
		}
	}
	return s.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": s.messageBlockIndex, "delta": delta})
}

// toolDelta 将分散的工具 ID、函数名和 JSON 参数片段归并到同一工具调用。
// 参数 index 为上游工具索引，id、name、args 为本帧增量；返回协议输出错误，consume/Google 工具转换调用。
// 首次出现时分配稳定输出块，同时为 Chat 分配从零连续的工具索引；上游稀疏索引不能直接泄露为 Chat 索引。
// ID 非空时更新，名称和参数按增量追加；参数完整性、必需 ID/名称在 finish 统一校验。
// 会修改 calls/blocks/toolIndices 并写流，不能跨请求复用或在终态失败后提交历史。
func (s *dialogueStream) toolDelta(index int, id, name, args string) error {
	if err := s.start(); err != nil {
		return err
	}
	call := s.calls[index]
	fresh := call == nil
	if fresh {
		call = &llmdialogue.Turn{}
		s.calls[index] = call
		s.toolIndices[index] = len(s.toolIndices)
		if s.protocol != "anthropic-messages" {
			s.blocks[index] = s.nextBlock
			s.nextBlock++
		}
	}
	if id != "" {
		call.ID = id
	}
	call.Name += name
	call.Arguments += args
	block := s.blocks[index]
	switch s.protocol {
	case "openai-chat":
		fn := map[string]any{}
		if name != "" {
			fn["name"] = name
		}
		if args != "" {
			fn["arguments"] = args
		}
		c := map[string]any{"index": s.toolIndices[index], "function": fn}
		if fresh || id != "" {
			c["id"] = call.ID
			c["type"] = "function"
		}
		return s.chat(map[string]any{"tool_calls": []any{c}}, nil)
	case "openai-responses":
		if len(s.toolBindings) > 0 {
			// LiteLLM 同样需要双向恢复 custom 输入；缓冲到完整参数后再生成对应生命周期事件。
			return nil
		}
		if fresh {
			if err := s.emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": block, "item": map[string]any{"id": llmdialogue.ResponseItemID("fc", call.ID), "type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": "", "status": "in_progress"}}); err != nil {
				return err
			}
		}
		if args != "" {
			return s.emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": block, "item_id": llmdialogue.ResponseItemID("fc", call.ID), "delta": args})
		}
	case "anthropic-messages":
		// Chat 可以交错多个工具片段，Messages 要求内容块有序；finish 校验完整 JSON 后逐个发送，保留各工具身份。
	}
	return nil
}

// observeUsage 保存上游流事件报告的用量，并归一输入/输出字段别名。
// 参数 u 为部分用量，可为空；返回值无，consume 的用量事件分支调用。
// 后到同名字段覆盖旧值而非累加，保留后续帧省略的字段；先标记原始别名冲突，不能靠覆盖恢复可计费状态。
// 只更新本请求 s.usage，不估算、不记账，缺少终态时这些已观测事实也不能使失败流成为成功。
func (s *dialogueStream) observeUsage(u map[string]any) {
	s.usage = llmdialogue.MergeDialogueUsage(s.usage, u)
	for source, target := range map[string]string{"input_tokens": "prompt_tokens", "output_tokens": "completion_tokens"} {
		if value, exists := u[source]; exists {
			s.usage[target] = value
		}
		delete(s.usage, source)
	}
}

// finish 验证累计回复并输出调用方协议要求的完成事件。
// 无参数；返回工具不完整、参数 JSON 非法或写出错误；PipeDialogue 确认上游完成后调用。
// 按稳定索引收集工具，校验 ID、名称与完整 JSON 参数，再归一用量和 stop 原因。
// 各协议使用自己的结束块/终态；Responses 最终 output 保留增量阶段索引，在 completed/incomplete 发布前调用 beforeComplete。
// 回调收到验证过的 DialogueResult，先保存续接归属再发布终态，避免客户端立即续接时找不到历史。
// 若随后终态写入失败，已保存归属不会回滚；回调不是客户端接收确认，也不能保存未验证的工具参数。
// 副作用为 HTTP 帧与回调状态写入，函数不能修复已发生的部分写出。
func (s *dialogueStream) finish() error {
	if s.refused {
		return s.finishRefusal()
	}
	if err := s.start(); err != nil {
		return err
	}
	s.usage = llmdialogue.NormalizeDialogueUsage(s.usage)
	r := llmdialogue.DialogueResult{ID: s.id, Text: s.text, Stop: s.stop, Usage: s.usage, Reasoning: s.reasoning, ToolBindings: s.toolBindings}
	if r.Stop == "" {
		r.Stop = "stop"
	}
	indices := make([]int, 0, len(s.calls))
	for i := range s.calls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	for _, i := range indices {
		if c := s.calls[i]; c != nil {
			// 流可以在任意参数片段后中断，必须等终态时验证完整 JSON，不能把半截调用交给客户端执行。
			if c.Arguments == "" {
				c.Arguments = "{}"
			}
			if c.ID == "" || c.Name == "" || !json.Valid([]byte(c.Arguments)) {
				return fmt.Errorf("incomplete upstream tool call")
			}
			r.Calls = append(r.Calls, *c)
		}
	}
	if s.messagesResponses {
		if s.messagesError != nil {
			return s.messagesError
		}
		if s.messagesVerified.Source != "anthropic-messages" {
			return fmt.Errorf("invalid Messages stream completion")
		}
		r = s.messagesVerified
		r.ToolBindings = s.toolBindings
		if s.prepareResult != nil {
			var err error
			r, err = s.prepareResult(r)
			if err != nil {
				return err
			}
		}
		if s.messagesReasoning && llmdialogue.HasOpaqueMessagesResult(r) {
			return fmt.Errorf("signed Messages history was not prepared")
		}
		if s.messagesReasoning && r.Source != "openai-responses" {
			s.messagesReasoning = false
			s.reasoningStarted, s.reasoningBlock, s.reasoning = true, 0, r.Reasoning
		}
		if s.messagesReasoning && len(streamList(r.Native["output"])) == 0 {
			return fmt.Errorf("signed Messages history result is empty")
		}
	}
	// 达到输出上限的工具回复仍是截断，不能被工具存在这一事实覆盖成可执行的正常结束。
	if len(r.Calls) > 0 && r.Stop != "length" {
		r.Stop = "tool_calls"
	}
	switch s.protocol {
	case "gemini", "vertex":
		// 文本已按增量发送；终态仅发送完整函数参数与用量，避免文本重复。
		r.Text = ""
		raw, err := llmdialogue.EncodeDialogueResult(r, s.protocol, s.model)
		if err != nil {
			return err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		return s.emit("", out)
	case "openai-chat":
		delta := map[string]any{}
		if len(s.chatReasoningItems) > 0 {
			delta["reasoning_items"] = s.chatReasoningItems
		}
		if s.beforeComplete != nil {
			// Chat 客户端收到工具终态即可回传扩展；先发布原连接归属，避免紧接的工具续轮出现竞态。
			s.beforeComplete(r)
		}
		if err := s.chat(delta, r.Stop); err != nil {
			return err
		}
		if err := s.emit("", map[string]any{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{}, "usage": s.usage}); err != nil {
			return err
		}
		s.capture.WriteString("data: [DONE]\n\n")
		_, err := io.WriteString(s.writer, "data: [DONE]\n\n")
		if f, ok := s.writer.(http.Flusher); ok {
			f.Flush()
		}
		return err
	case "openai-responses":
		raw, err := llmdialogue.EncodeDialogueResult(r, s.protocol, s.model)
		if err != nil {
			return err
		}
		var response map[string]any
		_ = json.Unmarshal(raw, &response)
		// 完成事件按已发送的输出索引重排，保留工具和文本块的生命周期及稳定 ID。
		items := make([]any, s.nextBlock)
		if s.messagesReasoning {
			item := streamMap(streamList(r.Native["output"])[0])
			item["id"] = llmdialogue.ResponseItemID("rs", s.responseItemSeed)
			items[0] = item
			if err := s.emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}); err != nil {
				return err
			}
		} else if s.reasoningStarted {
			part := map[string]any{"type": "summary_text", "text": s.reasoning}
			item := map[string]any{"id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "type": "reasoning", "summary": []any{part}}
			items[s.reasoningBlock] = item
			for _, event := range []map[string]any{
				{"type": "response.reasoning_summary_text.done", "item_id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "output_index": s.reasoningBlock, "summary_index": 0, "text": s.reasoning},
				{"type": "response.reasoning_summary_part.done", "item_id": llmdialogue.ResponseItemID("rs", s.responseItemSeed), "output_index": s.reasoningBlock, "summary_index": 0, "part": part},
				{"type": "response.output_item.done", "output_index": s.reasoningBlock, "item": item},
			} {
				if err := s.emit(streamString(event["type"]), event); err != nil {
					return err
				}
			}
		}
		if s.textBlock >= 0 {
			part := map[string]any{"type": "output_text", "text": s.text, "annotations": []any{}}
			item := map[string]any{"id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
			items[s.textBlock] = item
			for _, event := range []map[string]any{
				{"type": "response.output_text.done", "item_id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "output_index": s.textBlock, "content_index": 0, "text": s.text},
				{"type": "response.content_part.done", "item_id": llmdialogue.ResponseItemID("msg", s.responseItemSeed), "output_index": s.textBlock, "content_index": 0, "part": part},
				{"type": "response.output_item.done", "output_index": s.textBlock, "item": item},
			} {
				if err := s.emit(streamString(event["type"]), event); err != nil {
					return err
				}
			}
		}
		for _, index := range indices {
			c := s.calls[index]
			block := s.blocks[index]
			item, err := llmdialogue.ResponseToolItem(*c, s.toolBindings, "completed")
			if err != nil {
				return err
			}
			items[block] = item
			if item["type"] == "tool_search_call" {
				// 客户端搜索参数是完整 JSON 对象，不能冒充 function_call_arguments 的字符串 delta。
				// 和 LiteLLM 的专用搜索生命周期一样，仅发布 added/done 项，再由客户端执行并续轮。
				started := map[string]any{}
				for key, value := range item {
					started[key] = value
				}
				started["arguments"], started["status"] = map[string]any{}, "in_progress"
				for _, event := range []map[string]any{
					{"type": "response.output_item.added", "output_index": block, "item": started},
					{"type": "response.output_item.done", "output_index": block, "item": item},
				} {
					if err := s.emit(streamString(event["type"]), event); err != nil {
						return err
					}
				}
				continue
			}
			field, prefix := "arguments", "response.function_call_arguments"
			if item["type"] == "custom_tool_call" {
				field, prefix = "input", "response.custom_tool_call_input"
			}
			if len(s.toolBindings) > 0 {
				started := map[string]any{}
				for key, value := range item {
					started[key] = value
				}
				started[field], started["status"] = "", "in_progress"
				if err := s.emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": block, "item": started}); err != nil {
					return err
				}
				if err := s.emit(prefix+".delta", map[string]any{"type": prefix + ".delta", "item_id": item["id"], "output_index": block, "delta": item[field]}); err != nil {
					return err
				}
			}
			if err := s.emit(prefix+".done", map[string]any{"type": prefix + ".done", "item_id": item["id"], "output_index": block, field: item[field]}); err != nil {
				return err
			}
			if err := s.emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": block, "item": item}); err != nil {
				return err
			}
		}
		response["output"] = items
		if s.messagesReasoning {
			r.Native = response
		}
		event := "response.completed"
		if r.Stop == "length" {
			event = "response.incomplete"
		}
		if s.beforeComplete != nil {
			// 完成事件一旦可见客户端就可能立刻续接，因此先保存历史；这不是跨 HTTP/缓存的原子事务。
			s.beforeComplete(r)
		}
		return s.emit(event, map[string]any{"type": event, "response": response})
	case "anthropic-messages":
		for _, call := range r.Calls {
			if err := s.closeMessageBlock(); err != nil {
				return err
			}
			if err := s.messageDelta(map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}}, map[string]any{"type": "input_json_delta", "partial_json": call.Arguments}); err != nil {
				return err
			}
		}
		if err := s.closeMessageBlock(); err != nil {
			return err
		}
		stop := "end_turn"
		if r.Stop == "length" {
			stop = "max_tokens"
		}
		if len(r.Calls) > 0 && r.Stop != "length" {
			stop = "tool_use"
		}
		if s.beforeComplete != nil {
			// Messages 客户端收到工具终态即可续轮，先发布密文的原连接归属，避免立即重放时缓存尚未写入。
			s.beforeComplete(r)
		}
		if err := s.emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": llmdialogue.DialogueOutputUsage(s.usage, "anthropic-messages")}); err != nil {
			return err
		}
		return s.emit("message_stop", map[string]any{"type": "message_stop"})
	}
	return nil
}

// consume 按明确的上游协议解码单个完整 SSE data 事件。
// 参数 protocol 为登记协议，data 为事件文本；返回解析、供应商错误或调用方写出错误；PipeDialogue 逐事件调用。
// 解析 Chat、Responses、Messages、Google 的文本、工具、用量及终态，统一交给本请求输出状态机。
// 不从 URL 猜协议；DONE/协议结束标记才更新完成事实，网络 EOF 本身不能证明成功。
// 会更新响应 ID、stop、completed 和累计内容，并可能立即写出；工具/output item ID 不应覆盖响应级 ID。
func (s *dialogueStream) consume(protocol string, data string) error {
	if data == "[DONE]" {
		if protocol == "openai-chat" {
			// DONE 只是传输结束标志；没有明确停止原因时不能补造正常完成。
			if s.stop == "" {
				return fmt.Errorf("upstream stream ended without finish_reason")
			}
			s.completed = true
		}
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return err
	}
	if upstreamErr := dpdiagnostics.ExtractUpstreamError(m); upstreamErr != nil {
		return upstreamErr
	}
	if id := streamString(m["id"]); id != "" {
		s.id = id
	}
	s.observeUsage(streamMap(m["usage"]))
	typ := streamString(m["type"])
	if protocol == "anthropic-messages" && s.messagesError == nil {
		result, complete, err := s.messagesVerifier.consume(m)
		s.messagesError = err
		if complete {
			s.messagesVerified = result
		}
	}
	switch protocol {
	case "gemini", "vertex":
		if m["candidates"] == nil {
			// Google 可在单独末帧提供用量；不能把缺少候选的普通空帧视为完成。
			if streamMap(m["usageMetadata"]) != nil {
				m["candidates"] = []any{map[string]any{}}
				raw, _ := json.Marshal(m)
				result, err := llmdialogue.ParseDialogueResult(protocol, raw)
				if err != nil {
					return err
				}
				for k, v := range result.Usage {
					if k != "pricing_blocked" {
						s.usage[k] = v
					}
				}
				return nil
			}
			return fmt.Errorf("Google stream candidate missing")
		}
		r, err := llmdialogue.ParseDialogueResult(protocol, []byte(data))
		if err != nil {
			return err
		}
		if r.ID != "" {
			s.id = r.ID
		}
		for k, v := range r.Usage {
			if k != "pricing_blocked" {
				s.usage[k] = v
			}
		}
		if err := s.textDelta(r.Text); err != nil {
			return err
		}
		for _, c := range r.Calls {
			if err := s.googleCall(c); err != nil {
				return err
			}
		}
		choices, _ := m["candidates"].([]any)
		if len(choices) == 1 && streamString(streamMap(choices[0])["finishReason"]) != "" {
			s.completed = true
			s.stop = r.Stop
		}
	case "openai-chat":
		choices, _ := m["choices"].([]any)
		for _, v := range choices {
			choice := streamMap(v)
			if streamIndex(choice["index"]) != 0 {
				return fmt.Errorf("multiple stream choices unsupported")
			}
			delta := streamMap(choice["delta"])
			if delta["refusal"] != nil && !s.observeOnly {
				return fmt.Errorf("refusal stream requires native protocol")
			}
			if err := s.reasoningDelta(streamString(delta["reasoning_content"])); err != nil {
				return err
			}
			if err := s.textDelta(streamString(delta["content"])); err != nil {
				return err
			}
			calls, _ := delta["tool_calls"].([]any)
			for _, v := range calls {
				c := streamMap(v)
				f := streamMap(c["function"])
				if err := s.toolDelta(streamIndex(c["index"]), streamString(c["id"]), streamString(f["name"]), streamString(f["arguments"])); err != nil {
					return err
				}
			}
			if stop := streamString(choice["finish_reason"]); stop != "" {
				s.stop = stop
			}
		}
	case "openai-responses":
		if response := streamMap(m["response"]); response != nil {
			s.id = streamString(response["id"])
			s.observeUsage(streamMap(response["usage"]))
		}
		switch typ {
		case "response.refusal.delta":
			if !s.observeOnly {
				return fmt.Errorf("refusal stream requires native protocol")
			}
		case "response.reasoning_summary_text.delta":
			if s.responsesMessages {
				s.reasoning += streamString(m["delta"])
				s.responseReasoningPending = true
				return s.start()
			}
			return s.reasoningDelta(streamString(m["delta"]))
		case "response.output_text.delta":
			if s.responsesMessages && (s.responsePrelude || s.responseReasoningPending) {
				s.pendingResponseText += streamString(m["delta"])
				return nil
			}
			return s.textDelta(streamString(m["delta"]))
		case "response.output_item.added":
			item := streamMap(m["item"])
			if s.responsesMessages {
				s.responsePrelude = false
				if item["type"] == "reasoning" {
					s.responseReasoningPending = true
					return s.start()
				}
				if !s.responseReasoningPending {
					if err := s.flushResponseText(); err != nil {
						return err
					}
				}
			}
			if item["type"] == "function_call" {
				return s.toolDelta(streamIndex(m["output_index"]), streamString(item["call_id"]), streamString(item["name"]), streamString(item["arguments"]))
			}
		case "response.function_call_arguments.delta":
			return s.toolDelta(streamIndex(m["output_index"]), "", "", streamString(m["delta"]))
		case "response.output_item.done":
			item := streamMap(m["item"])
			if s.responsesMessages && item["type"] == "reasoning" {
				if err := s.responseReasoningItem(item, false); err != nil {
					return err
				}
				return s.flushResponseText()
			}
		case "response.completed", "response.incomplete":
			// 达到输出上限仍是可观察终态，必须与 completed 使用相同的密文/拒绝校验，不能静默降级为普通推理。
			response := streamMap(m["response"])
			if response == nil || streamString(response["id"]) == "" || response["status"] != strings.TrimPrefix(typ, "response.") {
				return fmt.Errorf("invalid upstream response terminal")
			}
			raw, _ := json.Marshal(response)
			var result llmdialogue.DialogueResult
			var err error
			if s.nativeResponses {
				result, err = llmdialogue.ParseNativeResponsesResult(raw)
			} else {
				result, err = llmdialogue.ParseDialogueResult(protocol, raw)
			}
			if err != nil {
				return err
			}
			// 即使没有可见增量也必须校验完整终态对象；observeOnly 同样不能放过损坏对象。
			if !s.observeOnly {
				if _, err = llmdialogue.EncodeDialogueResult(result, s.protocol, s.model); err != nil {
					return err
				}
				if s.protocol == "openai-chat" {
					s.chatReasoningItems, err = llmdialogue.ChatReasoningItems(result)
					if err != nil {
						return err
					}
				}
				if s.responsesMessages {
					// 终态复核已经发出的推理项，同时兼容只在 completed/incomplete 才给完整项的提供商。
					items, _ := response["output"].([]any)
					for _, value := range items {
						item := streamMap(value)
						if item["type"] == "reasoning" {
							if err = s.responseReasoningItem(item, true); err != nil {
								return err
							}
						}
					}
					s.reasoning = result.Reasoning
					if s.text == "" && s.pendingResponseText == "" {
						s.pendingResponseText = result.Text
					}
					if err = s.flushResponseText(); err != nil {
						return err
					}
				} else if s.reasoning == "" {
					if err = s.reasoningDelta(result.Reasoning); err != nil {
						return err
					}
				}
			}
			s.completed = true
			if typ == "response.incomplete" {
				s.stop = "length"
			}
		case "response.failed", "error":
			return fmt.Errorf("upstream response failed")
		}
	case "anthropic-messages":
		switch typ {
		case "message_start":
			msg := streamMap(m["message"])
			s.id = streamString(msg["id"])
			s.observeUsage(streamMap(msg["usage"]))
		case "content_block_start":
			b := streamMap(m["content_block"])
			switch b["type"] {
			case "text":
				return s.textDelta(streamString(b["text"]))
			case "tool_use":
				args := ""
				if input := streamMap(b["input"]); len(input) > 0 {
					raw, _ := json.Marshal(input)
					args = string(raw)
				}
				return s.toolDelta(streamIndex(m["index"]), streamString(b["id"]), streamString(b["name"]), args)
			case "thinking", "redacted_thinking":
				if s.messagesResponses {
					return s.startMessagesReasoning()
				}
				// 原生观察器另行完整校验签名和块生命周期；跨协议不伪造公开推理或厂商签名。
				if !s.observeOnly {
					return fmt.Errorf("unsupported upstream content block %v", b["type"])
				}
			default:
				return fmt.Errorf("unsupported upstream content block %v", b["type"])
			}
		case "content_block_delta":
			d := streamMap(m["delta"])
			switch d["type"] {
			case "text_delta":
				return s.textDelta(streamString(d["text"]))
			case "input_json_delta":
				return s.toolDelta(streamIndex(m["index"]), "", "", streamString(d["partial_json"]))
			case "thinking_delta", "signature_delta":
				if s.messagesResponses {
					return nil
				}
				if !s.observeOnly {
					return fmt.Errorf("unsupported upstream delta %v", d["type"])
				}
			default:
				return fmt.Errorf("unsupported upstream delta %v", d["type"])
			}
		case "message_delta":
			d := streamMap(m["delta"])
			if stop := streamString(d["stop_reason"]); stop != "" {
				if s.refused && stop != "refusal" {
					return fmt.Errorf("conflicting upstream refusal stop_reason")
				}
				s.stop = stop
			}
			if d["stop_reason"] == "refusal" {
				explanation, err := llmdialogue.MessagesRefusalExplanation(d["stop_details"])
				if err != nil {
					return err
				}
				if s.refused && s.refusal != explanation {
					return fmt.Errorf("conflicting upstream refusal explanation")
				}
				s.refused, s.refusal = true, explanation
			}
			if d["stop_reason"] == "max_tokens" {
				s.stop = "length"
			}
			// Messages 的自然结束和停止序列都对应 Chat stop；不能把供应商枚举泄露给 OpenAI 客户端。
			if d["stop_reason"] == "end_turn" || d["stop_reason"] == "stop_sequence" {
				s.stop = "stop"
			}
		case "message_stop":
			if s.stop == "" {
				return fmt.Errorf("upstream stream ended without stop_reason")
			}
			s.completed = true
		case "error":
			return fmt.Errorf("upstream message failed")
		}
	default:
		return fmt.Errorf("unsupported upstream stream protocol")
	}
	return nil
}

// googleCall 把 Google 一次性函数调用转换成统一工具增量。
// 参数 c 为包含函数 ID/名称/参数的 Turn；返回 toolDelta 写出错误；consume 的 Google 分支调用。
// 按已有调用身份去重，避免重复候选帧生成同一个工具两次；完整参数仍在 finish 校验。
// 修改工具状态并可能写出 SSE，不替调用方执行任何工具。
func (s *dialogueStream) googleCall(c llmdialogue.Turn) error {
	for _, existing := range s.calls {
		if existing.ID == c.ID {
			if existing.Name == c.Name && existing.Arguments == c.Arguments {
				return nil
			}
			return fmt.Errorf("conflicting Google function call ID")
		}
	}
	return s.toolDelta(len(s.calls), c.ID, c.Name, c.Arguments)
}

// PipeDialogue 将上游对话 SSE 解码为统一状态，再编码成调用方请求的协议。
// 参数 w 为客户端写入器，resp 为上游响应，start 为请求起点；alias 为公开模型，upstream/caller 为显式协议 ID；可选 beforeComplete 在有效终态发布前接收最终回复。
// 返回 wrote、usage、ttft、captured、err：是否实际写出、已观测用量、首数据耗时、拟发送帧捕获及错误。Serve 的适配流分支调用。
// 使用 SDK 的 SSE 帧解码器，不使用 SDK 客户端或重试；本函数负责关闭 resp.Body。
// 必须观察协议完成标记才调用 finish；EOF 无完成、供应商 error、非法工具参数或写出失败均不能作为成功回复。
// 已写出时尽力发协议失败事件并返回错误，执行器不得重试；未写出才可能安全尝试候选。
// 解码或验证失败不调用完成回调；若失败发生在回调后的终态写出，已保存的续接事实不会自动回滚。
// capture 不是客户端接收确认；状态和回调仅限单请求，不跨请求共享。
func PipeDialogue(w http.ResponseWriter, resp *http.Response, start time.Time, alias, upstream, caller string, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	return PipeDialogueWithTools(w, resp, start, alias, upstream, caller, nil, beforeComplete...)
}

// PipeDialogueWithTools 为真实请求附加 namespace/custom 工具还原表，其他参数、返回值及资源清理与 PipeDialogue 一致。
// Serve 调用；映射只作用于跨协议 Responses 工具事件，原生 SSE 不改；参数完整后发布工具输入，不执行工具或改变鉴权。
func PipeDialogueWithTools(w http.ResponseWriter, resp *http.Response, start time.Time, alias, upstream, caller string, bindings map[string]llmdialogue.ToolBinding, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	return PipeDialogueWithPreparation(w, resp, start, alias, upstream, caller, bindings, nil, beforeComplete...)
}

// PipeDialogueWithPreparation 在跨协议完成发布前执行可失败的持久化准备钩子。
// 参数与 WithTools 相同，prepare 接收完整原生结果并返回可发布结果；只用于 Messages→Responses，错误不能产生 done 或 completed。
// 同协议保持既有路径；prepare 不得泄露原厂签名，存储成功后客户端写失败无需撤销已保存的临时记录。
func PipeDialogueWithPreparation(w http.ResponseWriter, resp *http.Response, start time.Time, alias, upstream, caller string, bindings map[string]llmdialogue.ToolBinding, prepare func(llmdialogue.DialogueResult) (llmdialogue.DialogueResult, error), beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	if upstream == caller && (caller == "openai-chat" || caller == "openai-responses" || caller == "anthropic-messages") {
		return PipeNativeDialogue(w, resp, start, alias, caller, beforeComplete...)
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	util.PreventSSETransform(w.Header())
	s := dialogueStream{writer: w, protocol: caller, model: alias, calls: map[int]*llmdialogue.Turn{}, blocks: map[int]int{}, usage: map[string]any{}, textBlock: -1, toolIndices: map[int]int{}, toolBindings: bindings}
	if upstream == "anthropic-messages" && caller == "openai-responses" && prepare != nil {
		s.messagesResponses, s.prepareResult = true, prepare
	}
	if upstream == "openai-responses" && caller == "anthropic-messages" {
		s.responsesMessages, s.responsePrelude = true, true
		s.responseReasoningBlocks = map[string]string{}
	}
	if len(beforeComplete) > 0 {
		s.beforeComplete = beforeComplete[0]
	}
	// 官方 SSE 分帧器处理注释、多行 data 和 CRLF；不接受 EOF 前未分隔的残缺事件。
	// 只复用解析器，没有创建 SDK 客户端，不会引入自动重试。
	decoder := ssestream.NewDecoder(resp)
	var ttft time.Duration
	var err error
	for decoder.Next() {
		err = s.consume(upstream, strings.TrimSpace(string(decoder.Event().Data)))
		if s.wrote && ttft == 0 {
			ttft = time.Since(start)
		}
		if err != nil {
			break
		}
		// 三种对话协议有独立的最终结束事件，校验后立即结束，不能再等待 HTTP EOF。
		// Google 的 finishReason 后仍可能单独发送 usageMetadata，必须继续读取这类尾帧。
		if s.completed && upstream != "gemini" && upstream != "vertex" {
			break
		}
	}
	if err == nil {
		err = decoder.Err()
	}
	if err == nil && !s.completed {
		err = fmt.Errorf("upstream stream ended without completion")
	}
	if err == nil {
		err = s.finish()
	}
	if err != nil && s.wrote { // 已输出后发送明确失败事件，不能生成完成事件或改用另一个部署。
		event, payload := streamFailureEvent(err, caller, s.id)
		_ = s.emit(event, payload)
	}
	return s.wrote, s.usage, ttft, s.capture.Bytes(), err
}

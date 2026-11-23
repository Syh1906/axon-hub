package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/kaptinlin/jsonrepair"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

//nolint:maintidx // TODO: fix this.
func AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent, platformType PlatformType) ([]byte, llm.ResponseMeta, error) {
	a := newMessageAggregation(platformType)
	for _, ev := range chunks {
		a.Add(ctx, ev)
	}
	return a.Result()
}

type messageAggregation struct {
	text                           map[*string]*strings.Builder
	platformType                   PlatformType
	hasChunks                      bool
	messageStart                   *StreamEvent
	contentBlocks                  []MessageContentBlock
	usage                          *Usage
	stopReason, stopSequence       *string
	messageDeltaUsage, streamError bool
	usageEvidence                  llm.AccountingEvidence
}

// Block text arrives as many short deltas. Keep each result field current
// without copying the entire prefix again for every token.
func (a *messageAggregation) appendText(target *string, delta string) {
	if delta == "" {
		return
	}
	if a.text == nil {
		a.text = make(map[*string]*strings.Builder)
	}
	b := a.text[target]
	if b == nil {
		b = new(strings.Builder)
		b.WriteString(*target)
		a.text[target] = b
	}
	b.WriteString(delta)
	*target = b.String()
}

func newMessageAggregation(platform PlatformType) *messageAggregation {
	return &messageAggregation{platformType: platform, usageEvidence: llm.AccountingEvidenceUnavailable}
}
func (a *messageAggregation) Add(ctx context.Context, chunk *httpclient.StreamEvent) {
	a.hasChunks = true
	var event StreamEvent

	err := json.Unmarshal(chunk.Data, &event)
	if err != nil {
		return // Skip invalid chunks
	}

	// log.Debug(ctx, "chat stream event", log.Any("event", event))

	switch event.Type {
	case "message_start":
		if event.Message != nil {
			a.messageStart = &event
			if event.Message.Usage != nil {
				a.usage = event.Message.Usage
			}
		}
	case "content_block_start":
		if event.ContentBlock != nil {
			block := *event.ContentBlock
			// For tool-use-like blocks, reset Input to nil so it is built
			// from subsequent input_json_delta events.
			if isAnthropicToolUseLike(block.Type) {
				block.Input = nil
			}
			// redacted_thinking blocks come complete in content_block_start
			// with their Data field already populated.
			// *_tool_result blocks also arrive complete (content + caller);
			// we preserve them as-is.

			a.contentBlocks = append(a.contentBlocks, block)
		}
	case "content_block_delta":
		if event.Index != nil && *event.Index >= 0 {
			index := int(*event.Index)
			// Ensure we have enough content blocks
			for len(a.contentBlocks) <= index {
				a.contentBlocks = append(a.contentBlocks, MessageContentBlock{Type: "text", Text: lo.ToPtr("")})
			}

			if event.Delta != nil {
				if event.Delta.Text != nil {
					if a.contentBlocks[index].Type == "text" {
						if a.contentBlocks[index].Text == nil {
							a.contentBlocks[index].Text = lo.ToPtr("")
						}

						a.appendText(a.contentBlocks[index].Text, *event.Delta.Text)
					}
				}

				if event.Delta.Citation != nil {
					if a.contentBlocks[index].Type == "text" {
						a.contentBlocks[index].Citations = append(a.contentBlocks[index].Citations, *event.Delta.Citation)
					}
				}

				if event.Delta.Thinking != nil {
					if a.contentBlocks[index].Type == "thinking" {
						if a.contentBlocks[index].Thinking == nil {
							a.contentBlocks[index].Thinking = lo.ToPtr("")
						}

						a.appendText(a.contentBlocks[index].Thinking, *event.Delta.Thinking)
					} else {
						// Convert to thinking block if it's not already
						a.contentBlocks[index].Type = "thinking"
						a.contentBlocks[index].Thinking = event.Delta.Thinking
					}
				}

				if event.Delta.Signature != nil {
					// Handle signature delta - append to thinking block signature
					if a.contentBlocks[index].Type == "thinking" {
						if event.Delta.Signature != nil {
							if a.contentBlocks[index].Signature == nil {
								a.contentBlocks[index].Signature = event.Delta.Signature
							} else {
								a.appendText(a.contentBlocks[index].Signature, *event.Delta.Signature)
							}
						}
					} else {
						// Convert to thinking block if it's not already
						a.contentBlocks[index].Type = "thinking"
						a.contentBlocks[index].Signature = event.Delta.Signature
					}
				}

				if event.Delta.PartialJSON != nil {
					switch {
					case isAnthropicToolUseLike(a.contentBlocks[index].Type):
						if a.contentBlocks[index].Input == nil {
							a.contentBlocks[index].Input = []byte(*event.Delta.PartialJSON)
						} else {
							a.contentBlocks[index].Input = append(a.contentBlocks[index].Input, []byte(*event.Delta.PartialJSON)...)
						}
					case a.contentBlocks[index].Type == "text":
						if a.contentBlocks[index].Text == nil {
							a.contentBlocks[index].Text = lo.ToPtr("")
						}

						a.appendText(a.contentBlocks[index].Text, *event.Delta.PartialJSON)
					}
				}
			}
		}
	case "message_delta":
		if event.Delta != nil {
			if event.Delta.StopSequence != nil {
				a.stopSequence = event.Delta.StopSequence
			}
			if event.Delta.StopReason != nil {
				a.stopReason = event.Delta.StopReason
			}
		}

		if event.Usage != nil {
			a.messageDeltaUsage = true
			if a.usage == nil {
				a.usage = event.Usage
			} else {
				// Merge usage information from message_delta with message_start
				// Keep input tokens from message_start, update output tokens from message_delta
				a.usage.OutputTokens = event.Usage.OutputTokens
				if event.Usage.InputTokens > 0 {
					a.usage.InputTokens = event.Usage.InputTokens
				}

				if event.Usage.CachedTokens > 0 {
					a.usage.CachedTokens = event.Usage.CachedTokens
					a.usage.InputTokens -= event.Usage.CacheReadInputTokens
				}

				if event.Usage.CacheCreationInputTokens > 0 {
					a.usage.CacheCreationInputTokens = event.Usage.CacheCreationInputTokens
				}

				if event.Usage.CacheReadInputTokens > 0 {
					a.usage.CacheReadInputTokens = event.Usage.CacheReadInputTokens
				}
			}
		}
	case "content_block_stop":
		if event.Index != nil && *event.Index >= 0 && int(*event.Index) < len(a.contentBlocks) {
			index := int(*event.Index)

			block := &a.contentBlocks[index]
			if isAnthropicToolUseLike(block.Type) {
				if !json.Valid(block.Input) {
					slog.WarnContext(ctx, "invalid tool use input", slog.String("input", string(block.Input)))

					repaired, err := jsonrepair.Repair(string(block.Input))
					if err == nil {
						block.Input = []byte(repaired)
					}
				}
			}
		}
	case "message_stop":
		if a.messageStart != nil && a.messageDeltaUsage && !a.streamError {
			a.usageEvidence = llm.AccountingEvidenceExplicitFinal
		}
	case "error":
		a.streamError = true
		// An error before message_stop means the message never completed, so
		// its usage may be partial. An error after message_stop cannot retract
		// usage the provider already delivered with a completed message.
		if a.usageEvidence != llm.AccountingEvidenceExplicitFinal {
			a.usageEvidence = llm.AccountingEvidenceUnavailable
		}
	}
}

func (a *messageAggregation) Result() ([]byte, llm.ResponseMeta, error) {
	if !a.hasChunks {
		return nil, llm.ResponseMeta{}, errors.New("empty stream chunks")
	}

	// If no message_start event, create a default message
	var message *Message

	if a.messageStart != nil {
		// Ensure we have at least one content block
		if len(a.contentBlocks) == 0 {
			a.contentBlocks = []MessageContentBlock{
				{Type: "text", Text: lo.ToPtr("")},
			}
		}

		message = &Message{
			ID:           a.messageStart.Message.ID,
			Type:         a.messageStart.Message.Type,
			Role:         a.messageStart.Message.Role,
			Content:      a.contentBlocks,
			Model:        a.messageStart.Message.Model,
			Container:    a.messageStart.Message.Container,
			StopReason:   a.stopReason,
			StopSequence: a.stopSequence,
			Usage:        a.usage,
		}
	} else {
		// Ensure we have at least one content block
		if len(a.contentBlocks) == 0 {
			a.contentBlocks = []MessageContentBlock{
				{Type: "text", Text: lo.ToPtr("")},
			}
		}

		// Create a default message when no message_start event is received
		message = &Message{
			ID:           "msg_unknown",
			Type:         "message",
			Role:         "assistant",
			Content:      a.contentBlocks,
			Model:        "claude-3-sonnet-20240229",
			StopReason:   a.stopReason,
			StopSequence: a.stopSequence,
			Usage:        a.usage,
		}
	}

	data, err := json.Marshal(message)
	if err != nil {
		return nil, llm.ResponseMeta{}, err
	}

	// Convert and return usage if available
	if a.usage != nil {
		return data, llm.ResponseMeta{
			ID:                 message.ID,
			Usage:              convertToLlmUsage(a.usage, a.platformType),
			Completed:          a.messageStart != nil && a.stopReason != nil && *a.stopReason != "",
			AccountingEvidence: a.usageEvidence,
		}, nil
	}

	return data, llm.ResponseMeta{
		ID:                 message.ID,
		Completed:          a.messageStart != nil && a.stopReason != nil && *a.stopReason != "",
		AccountingEvidence: llm.AccountingEvidenceUnavailable,
	}, nil
}

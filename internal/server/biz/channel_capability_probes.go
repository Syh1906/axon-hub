package biz

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"github.com/google/uuid"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

const (
	probeImageTool = "responses/image_generation"
	probeWebSearch = "responses/web_search"
)

func endpointProbeFormats(t channel.Type) []string {
	if t == channel.TypeTypesafe {
		return []string{string(llm.APIFormatTypeSafeSystemOne)}
	}
	formats := []string{"openai/alpha_search", probeImageTool, "openai/responses-ws", probeWebSearch,
		"openai/image_generation", "openai/responses", "openai/chat_completions", "anthropic/messages", "gemini/contents",
		"openai/image_edit", "openai/image_variation", "openai/embeddings", "openai/moderations", "openai/decisions",
		"openai/audio_speech", "openai/audio_transcriptions", "openai/audio_translations", "gemini/embeddings",
		"jina/rerank", "jina/embeddings"}
	if supportsSystemOneEndpoint(t) {
		formats = append(formats, string(llm.APIFormatTypeSafeSystemOne))
	}
	if isZenmuxChannelType(t) {
		formats = append(formats, "zenmux/video")
	}
	for _, ep := range DefaultEndpointsForChannelType(t) {
		if !slices.Contains(formats, ep.APIFormat) {
			formats = append(formats, ep.APIFormat)
		}
	}
	return formats
}

func probeOutboundFormat(capability string) string {
	if capability == probeImageTool || capability == probeWebSearch {
		return string(llm.APIFormatOpenAIResponse)
	}
	return capability
}

// An unconfigured WebSocket probe follows the configured Responses path and URL.
func probeEndpoint(entity *ent.Channel, format string) objects.ChannelEndpoint {
	endpoints := NormalizeEndpoints(entity.Endpoints, entity.BaseURL)
	for _, ep := range endpoints {
		if ep.APIFormat == format {
			return ep
		}
	}
	if format == string(llm.APIFormatOpenAIResponseWebSocket) {
		for _, ep := range endpoints {
			if ep.APIFormat == string(llm.APIFormatOpenAIResponse) {
				ep.APIFormat = format
				return ep
			}
		}
	}
	return objects.ChannelEndpoint{APIFormat: format}
}

func buildCapabilityProbe(format, model string) *llm.Request {
	r := buildEndpointDetectRequest(model)
	r.APIFormat = llm.APIFormat(probeOutboundFormat(format))
	r.RequestType = llm.RequestTypeChat
	switch format {
	case "openai/responses", "openai/responses-ws":
		r.MaxCompletionTokens = lo.ToPtr(int64(32))
	case probeImageTool, probeWebSearch:
		tool := llm.Tool{Type: llm.ToolTypeWebSearch, WebSearch: &llm.WebSearch{}}
		prompt := "Search the web for the Go programming language."
		if format == probeImageTool {
			tool = llm.Tool{Type: llm.ToolTypeImageGeneration, ImageGeneration: &llm.ImageGeneration{}}
			prompt = "Generate one small image of a blue dot."
		}
		r.Messages[0].Content.Content = &prompt
		r.MaxCompletionTokens = nil
		r.Tools = []llm.Tool{tool}
		r.ToolChoice = &llm.ToolChoice{ToolChoice: lo.ToPtr("required")}
	case "openai/alpha_search":
		r.RequestType = llm.RequestTypeAlphaSearch
		id := uuid.NewString()
		r.AlphaSearch = &llm.AlphaSearchRequest{Body: []byte(fmt.Sprintf(`{"id":%q,"commands":{"search_query":[{"q":"Go programming language"}]}}`, id))}
	case "openai/completions":
		r.RequestType = llm.RequestTypeCompletion
		r.Completion = &llm.CompletionRequest{Prompt: []byte(`"Reply OK"`), MaxTokens: lo.ToPtr(int64(16))}
	case "openai/responses_compact":
		r.RequestType = llm.RequestTypeCompact
		r.Compact = &llm.CompactRequest{Input: r.Messages}
	case "modelscope/image_generation":
		r.RequestType = llm.RequestTypeImage
		r.APIFormat = llm.APIFormatOpenAIImageGeneration
		r.Image = &llm.ImageRequest{ModelSpecified: true, Prompt: "A blue dot"}
	case "openai/image_generation", "openai/image_edit", "openai/image_variation":
		r.RequestType = llm.RequestTypeImage
		r.Image = &llm.ImageRequest{ModelSpecified: true, Prompt: "A blue dot", N: lo.ToPtr(int64(1))}
		if format != "openai/image_generation" {
			pixel, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYPjPAAACAwEAKOj8bQAAAABJRU5ErkJggg==")
			r.Image.Images = [][]byte{pixel}
		}
	case "openai/embeddings", "gemini/embeddings", "jina/embeddings":
		r.RequestType = llm.RequestTypeEmbedding
		r.Embedding = &llm.EmbeddingRequest{Input: llm.EmbeddingInput{String: "hello"}}
	case "jina/rerank":
		r.RequestType = llm.RequestTypeRerank
		r.Rerank = &llm.RerankRequest{Query: "hello", Documents: []string{"hello"}}
	case "typesafe/systemone":
		r.RequestType = llm.RequestTypeSystemOne
		r.SystemOne = &llm.SystemOneRequest{State: "hello", Questions: map[string]llm.SystemOneQuestion{"greeting": {Type: "score", Instructions: "Is the text a greeting?"}}}
	case "openai/decisions":
		r.RequestType = llm.RequestTypeDecisions
		r.Decisions = &llm.DecisionsRequest{Body: []byte(`{"input":"hello","questions":[{"type":"choice","name":"greeting","instructions":"Is this a greeting?","choices":[{"value":"yes"},{"value":"no"}]}]}`)}
	case "openai/moderations":
		r.RequestType = llm.RequestTypeModeration
		r.Moderation = &llm.ModerationRequest{Input: llm.ModerationInput{String: "hello"}}
	case "openai/audio_speech":
		r.RequestType = llm.RequestTypeSpeech
		r.Speech = &llm.SpeechRequest{Input: "Hi", Voice: "alloy", ResponseFormat: "wav"}
	case "openai/audio_transcriptions":
		r.RequestType = llm.RequestTypeTranscription
		r.Transcription = &llm.TranscriptionRequest{File: probeWAV(), FileName: "silence.wav", ResponseFormat: "json"}
	case "openai/audio_translations":
		r.RequestType = llm.RequestTypeTranslation
		r.Translation = &llm.TranslationRequest{File: probeWAV(), FileName: "silence.wav", ResponseFormat: "json"}
	case "zenmux/video", "openai/video", "seedance/video":
		r.RequestType = llm.RequestTypeVideo
		r.Video = &llm.VideoRequest{Model: model, Content: []llm.VideoContent{{Type: "text", Text: "A blue dot."}}}
	}
	return r
}

// One second of 8 kHz, 16-bit mono silence, in a PCM WAV container.
func probeWAV() []byte {
	const dataSize = 16000
	body := make([]byte, 44+dataSize)
	copy(body, "RIFF")
	binary.LittleEndian.PutUint32(body[4:], 36+dataSize)
	copy(body[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(body[16:], 16)
	binary.LittleEndian.PutUint16(body[20:], 1)
	binary.LittleEndian.PutUint16(body[22:], 1)
	binary.LittleEndian.PutUint32(body[24:], 8000)
	binary.LittleEndian.PutUint32(body[28:], 16000)
	binary.LittleEndian.PutUint16(body[32:], 2)
	binary.LittleEndian.PutUint16(body[34:], 16)
	copy(body[36:], "data")
	binary.LittleEndian.PutUint32(body[40:], dataSize)
	return body
}

func validCapabilityResponse(format string, resp *httpclient.Response) bool {
	body := resp.Body
	if format == "openai/audio_speech" {
		return len(body) > 44 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WAVE"))
	}
	if !gjson.ValidBytes(body) {
		return false
	}
	doc := gjson.ParseBytes(body)
	if doc.Get("error").Type != gjson.Null {
		return false
	}
	switch format {
	case probeImageTool, probeWebSearch:
		if doc.Get("object").Str != "response" || doc.Get("status").Str != "completed" {
			return false
		}
		for _, item := range doc.Get("output").Array() {
			if item.Get("status").Str != "completed" {
				continue
			}
			if format == probeImageTool && item.Get("type").Str == "image_generation_call" && probeImageData(item.Get("result").Str) {
				return true
			}
			if format == probeWebSearch && item.Get("type").Str == "web_search_call" && item.Get("action.type").Str == "search" {
				return true
			}
		}
	case "openai/responses-ws":
		return doc.Get("object").Str == "response" && doc.Get("status").Str == "completed" && doc.Get("output").IsArray()
	case "openai/alpha_search":
		for _, item := range doc.Get("results").Array() {
			if item.IsObject() && (item.Get("title").Str != "" || item.Get("url").Str != "" || item.Get("text").Str != "") {
				return true
			}
		}
	case "gemini/contents":
		return len(doc.Get("candidates.0.content.parts").Array()) > 0
	case "openai/image_generation", "openai/image_edit", "openai/image_variation":
		for _, item := range doc.Get("data").Array() {
			u := item.Get("url").Str
			if validProbeImage(u, item.Get("b64_json").Str) {
				return true
			}
		}
	case "openai/embeddings", "jina/embeddings":
		return doc.Get("data.0.embedding.0").Type == gjson.Number
	case "gemini/embeddings":
		return doc.Get("embedding.values.0").Type == gjson.Number || doc.Get("embeddings.0.values.0").Type == gjson.Number
	case "openai/moderations":
		f := doc.Get("results.0.flagged")
		return f.Type == gjson.False || f.Type == gjson.True
	case "openai/decisions":
		return doc.Get("answers.0.name").Str == "greeting" && doc.Get("answers.0.choice").Exists()
	case "typesafe/systemone":
		return doc.Get("answers.greeting.score").Type == gjson.Number
	case "jina/rerank":
		return doc.Get("results.0.index").Type == gjson.Number && doc.Get("results.0.relevance_score").Type == gjson.Number
	case "openai/audio_transcriptions", "openai/audio_translations":
		return doc.Get("text").Type == gjson.String
	case "openai/completions":
		return doc.Get("object").Str == "text_completion" && doc.Get("choices.0.text").Type == gjson.String
	case "openai/responses_compact":
		return doc.Get("object").Str == "response.compaction" && len(doc.Get("output").Array()) > 0
	case "ollama/chat":
		return doc.Get("done").Bool() && doc.Get("message.role").Str == "assistant" && doc.Get("message.content").Str != ""
	case "openai/video":
		return doc.Get("object").Str == "video" && doc.Get("id").Str != "" && slices.Contains([]string{"queued", "in_progress", "completed"}, doc.Get("status").Str)
	case "seedance/video":
		return strings.HasPrefix(doc.Get("id").Str, "cgt-")
	case "zenmux/video":
		return doc.Get("id").Str != "" && slices.Contains([]string{"queued", "running", "succeeded"}, doc.Get("status").Str)
	default:
		return validEndpointDetectResponse(format, body)
	}
	return false
}

func validProbeImage(url, encoded string) bool {
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || probeImageData(encoded)
}

func probeImageData(encoded string) bool {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	config, _, err := image.DecodeConfig(io.LimitReader(decoder, 1<<20))
	return err == nil && config.Width > 0 && config.Height > 0
}

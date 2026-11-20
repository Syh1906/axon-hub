package orchestrator

import (
	"context"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// applyForceOneHourPromptCache extends existing Anthropic breakpoints after body
// pass-through but before channel body overrides, which retain final precedence.
func applyForceOneHourPromptCache(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("force-one-hour-prompt-cache", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		channel := outbound.GetCurrentChannel()
		if channel == nil || channel.Settings == nil || !channel.Settings.ForceOneHourPromptCache ||
			request == nil || request.APIFormat != string(llm.APIFormatAnthropicMessage) {
			return request, nil
		}

		if !gjson.ValidBytes(request.Body) {
			log.Warn(ctx, "skipping one-hour prompt cache for invalid JSON request body",
				log.String("channel", channel.Name), log.Int("channel_id", channel.ID))
			return request, nil
		}

		var paths []string
		collectOneHourCacheTTL(gjson.GetBytes(request.Body, "cache_control"), "cache_control", &paths)
		collectOneHourCacheArray(gjson.GetBytes(request.Body, "tools"), "tools", &paths, false)
		collectOneHourCacheArray(gjson.GetBytes(request.Body, "system"), "system", &paths, false)
		messages := gjson.GetBytes(request.Body, "messages")
		if messages.IsArray() {
			index := 0
			messages.ForEach(func(_, message gjson.Result) bool {
				collectOneHourCacheArray(message.Get("content"), "messages."+strconv.Itoa(index)+".content", &paths, true)
				index++
				return true
			})
		}

		if len(paths) == 0 {
			return request, nil
		}

		// sjson.SetBytes may write into the input's spare capacity. A previous attempt
		// or the inbound raw request can share this backing array, so copy before patching.
		body := append([]byte(nil), request.Body...)
		for _, path := range paths {
			var err error
			body, err = sjson.SetBytes(body, path, "1h")
			if err != nil {
				log.Warn(ctx, "failed to set one-hour prompt cache TTL",
					log.String("channel", channel.Name), log.Int("channel_id", channel.ID), log.Cause(err))
				return request, nil
			}
		}
		request.Body = body
		return request, nil
	})
}

func collectOneHourCacheTTL(control gjson.Result, path string, paths *[]string) {
	if control.IsObject() && control.Get("type").String() == "ephemeral" &&
		control.Get("ttl").String() != "1h" {
		*paths = append(*paths, path+".ttl")
	}
}

// nestedContent walks only content arrays on content blocks, including tool results.
func collectOneHourCacheArray(array gjson.Result, path string, paths *[]string, nestedContent bool) {
	if !array.IsArray() {
		return
	}
	index := 0
	array.ForEach(func(_, entry gjson.Result) bool {
		entryPath := path + "." + strconv.Itoa(index)
		collectOneHourCacheTTL(entry.Get("cache_control"), entryPath+".cache_control", paths)
		if nestedContent {
			collectOneHourCacheArray(entry.Get("content"), entryPath+".content", paths, true)
		}
		index++
		return true
	})
}

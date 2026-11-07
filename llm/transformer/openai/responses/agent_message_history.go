package responses

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NormalizeAgentMessageHistory repairs only content independently identified as
// plaintext by an earlier collaboration call in the same request. Missing
// encrypted_function_args, a familiar ciphertext prefix, or readable-looking
// bytes are not evidence. Unknown content and native agent-message identities
// remain intact. The original request buffer is never modified.
func NormalizeAgentMessageHistory(body []byte) ([]byte, error) {
	items := gjson.GetBytes(body, "input").Array()
	type delivery struct {
		target string
		spawn  bool
	}
	plaintext := make(map[string][]delivery)
	result := body
	for index, item := range items {
		if item.Get("type").String() == "function_call" && item.Get("namespace").String() == "collaboration" {
			switch item.Get("name").String() {
			case "spawn_agent", "send_message", "followup_task":
				marker := item.Get("encrypted_function_args")
				if marker.IsArray() && len(marker.Array()) == 0 {
					var args struct {
						Message  string `json:"message"`
						Target   string `json:"target"`
						TaskName string `json:"task_name"`
					}
					if json.Unmarshal([]byte(item.Get("arguments").String()), &args) == nil && args.Message != "" {
						to := delivery{target: args.Target, spawn: item.Get("name").String() == "spawn_agent"}
						if to.spawn {
							to.target = args.TaskName
						}
						if to.target != "" {
							plaintext[args.Message] = append(plaintext[args.Message], to)
						}
					}
				}
			}
		}
		if item.Get("type").String() != "agent_message" {
			continue
		}
		author, recipient := item.Get("author").String(), item.Get("recipient").String()
		if author == "" || recipient == "" {
			continue
		}
		for partIndex, part := range item.Get("content").Array() {
			value := part.Get("encrypted_content")
			if part.Get("type").String() != "encrypted_content" || value.Type != gjson.String {
				continue
			}
			matched := false
			for _, to := range plaintext[value.String()] {
				// Full recipient paths and children relative to the sender are
				// unambiguous. Opaque IDs and other aliases need client-side repair.
				if !to.spawn && to.target == recipient || strings.TrimRight(author, "/")+"/"+to.target == recipient {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			path := "input." + strconv.Itoa(index) + ".content." + strconv.Itoa(partIndex)
			var err error
			result, err = sjson.SetBytes(result, path+".type", "input_text")
			if err != nil {
				return nil, err
			}
			result, err = sjson.SetBytes(result, path+".text", value.String())
			if err != nil {
				return nil, err
			}
			result, err = sjson.DeleteBytes(result, path+".encrypted_content")
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

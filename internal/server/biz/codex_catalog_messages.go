package biz

// Message-only wire projection of OpenAI Codex rust-v0.166.0:
// codex-rs/protocol/src/openai_models.rs and its memento_v2,
// incremental_tools and guardian_v2 modules. Unknown JSON fields are ignored
// here and preserved in the original catalog map; this is not an output DTO.

import (
	"encoding/json"
	"fmt"
)

type codexWireString string

func (v *codexWireString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("null model message string")
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*v = codexWireString(raw)
	return nil
}

type codexWireBool bool

func (v *codexWireBool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("null model message boolean")
	}
	var raw bool
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*v = codexWireBool(raw)
	return nil
}

type codexWireInt int64

func (v *codexWireInt) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("null model message integer")
	}
	var raw int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*v = codexWireInt(raw)
	return nil
}

type codexWireClassifierMode string

func (v *codexWireClassifierMode) UnmarshalJSON(data []byte) error {
	var raw codexWireString
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw != "snapshot" && raw != "conversation" {
		return fmt.Errorf("invalid classifier mode")
	}
	*v = codexWireClassifierMode(raw)
	return nil
}

type codexWireModelMessages struct {
	ContentFilterGuidance  *codexWireString                    `json:"content_filter_guidance"`
	PersistentInstructions *codexWireString                    `json:"persistent_instructions"`
	Tools                  *codexWireToolMessages              `json:"tools"`
	InstructionsTemplate   *codexWireString                    `json:"instructions_template"`
	Approvals              *codexWireApprovalMessages          `json:"approvals"`
	CollaborationModes     *codexWireCollaborationModeMessages `json:"collaboration_modes"`
	AutoReview             *codexWireAutoReviewMessages        `json:"auto_review"`
	Permissions            *codexWirePermissionMessages        `json:"permissions"`
	MultiAgent             *codexWireMultiAgentMessages        `json:"multi_agent"`
	TokenBudget            *codexWireModelTokenBudgetConfig    `json:"token_budget"`
	GuardianV2             *codexWireGuardianV2ModelConfig     `json:"guardian_v2"`
	ConfirmationPolicies   *codexWireConfirmationPolicies      `json:"confirmation_policies"`
}

type codexWireToolMessages struct {
	History                                        *codexWireHistoryToolMessages           `json:"history"`
	Notes                                          *codexWireNotesToolMessages             `json:"notes"`
	ContextManagement                              *codexWireContextManagementToolMessages `json:"context_management"`
	IncrementalTools                               *codexWireIncrementalToolMessages       `json:"incremental_tools"`
	FunctionsNamespaceFunctionsDescriptionPrefixes *map[string]codexWireString             `json:"functions_namespace_functions_description_prefixes"`
	IndirectDescriptionPrefixes                    *codexWireIndirectDescriptionPrefixes   `json:"indirect_description_prefixes"`
	SendUserMessageAsync                           *codexWireToolMessage                   `json:"send_user_message_async"`
	MultiAgent                                     *codexWireMultiAgentToolMessages        `json:"multi_agent"`
	CodeMode                                       *codexWireCodeModeToolMessages          `json:"code_mode"`
	McpResources                                   *codexWireMcpResourceToolMessages       `json:"mcp_resources"`
}

type codexWireHistoryToolMessages struct {
	NamespaceDescription *codexWireString      `json:"namespace_description"`
	ListWindows          *codexWireToolMessage `json:"list_windows"`
	ListItems            *codexWireToolMessage `json:"list_items"`
	ReadItem             *codexWireToolMessage `json:"read_item"`
	SearchContents       *codexWireToolMessage `json:"search_contents"`
}

type codexWireToolMessage struct {
	Description *codexWireString `json:"description"`
	Parameters  *codexWireString `json:"parameters"`
}

type codexWireNotesToolMessages struct {
	NamespaceDescription *codexWireString      `json:"namespace_description"`
	ListFilesByPrefix    *codexWireToolMessage `json:"list_files_by_prefix"`
	ReadFile             *codexWireToolMessage `json:"read_file"`
	SearchContents       *codexWireToolMessage `json:"search_contents"`
	AppendToFile         *codexWireToolMessage `json:"append_to_file"`
	WriteFile            *codexWireToolMessage `json:"write_file"`
}

type codexWireContextManagementToolMessages struct {
	NewContext          *codexWireToolMessage `json:"new_context"`
	GetContextRemaining *codexWireToolMessage `json:"get_context_remaining"`
}

type codexWireIncrementalToolMessages struct {
	ToolUpdateHint               *codexWireString `json:"tool_update_hint"`
	RemovedToolsHeader           *codexWireString `json:"removed_tools_header"`
	RemovedNamespacesHeader      *codexWireString `json:"removed_namespaces_header"`
	NamespaceInstructionsPrefix  *codexWireString `json:"namespace_instructions_prefix"`
	NamespaceInstructionsCleared *codexWireString `json:"namespace_instructions_cleared"`
}

type codexWireIndirectDescriptionPrefixes struct {
	Namespaces *map[string]codexWireString `json:"namespaces"`
	McpServers *map[string]codexWireString `json:"mcp_servers"`
}

type codexWireMultiAgentToolMessages struct {
	SpawnAgent     *codexWireToolMessage `json:"spawn_agent"`
	SendMessage    *codexWireToolMessage `json:"send_message"`
	FollowupTask   *codexWireToolMessage `json:"followup_task"`
	WaitAgent      *codexWireToolMessage `json:"wait_agent"`
	InterruptAgent *codexWireToolMessage `json:"interrupt_agent"`
	ListAgents     *codexWireToolMessage `json:"list_agents"`
	CreateChannel  *codexWireToolMessage `json:"create_channel"`
	GetChannels    *codexWireToolMessage `json:"get_channels"`
	ListThreads    *codexWireToolMessage `json:"list_threads"`
	SearchPosts    *codexWireToolMessage `json:"search_posts"`
	ReadThread     *codexWireToolMessage `json:"read_thread"`
	ReadPost       *codexWireToolMessage `json:"read_post"`
	Subscribe      *codexWireToolMessage `json:"subscribe"`
	Unsubscribe    *codexWireToolMessage `json:"unsubscribe"`
	Post           *codexWireToolMessage `json:"post"`
}

type codexWireCodeModeToolMessages struct {
	Exec                        *codexWireToolMessage `json:"exec"`
	Wait                        *codexWireToolMessage `json:"wait"`
	DeferredNestedToolsGuidance *codexWireString      `json:"deferred_nested_tools_guidance"`
	McpTypescriptPreamble       *codexWireString      `json:"mcp_typescript_preamble"`
}

type codexWireMcpResourceToolMessages struct {
	ListMcpResources         *codexWireToolMessage `json:"list_mcp_resources"`
	ListMcpResourceTemplates *codexWireToolMessage `json:"list_mcp_resource_templates"`
	ReadMcpResource          *codexWireToolMessage `json:"read_mcp_resource"`
}

type codexWireApprovalMessages struct {
	OnRequest              *codexWireString                   `json:"on_request"`
	OnRequestAutoReview    *codexWireString                   `json:"on_request_auto_review"`
	Never                  *codexWireString                   `json:"never"`
	UnlessTrusted          *codexWireString                   `json:"unless_trusted"`
	RequestPermissions     *codexWireString                   `json:"request_permissions"`
	RequestPermissionsTool *codexWireString                   `json:"request_permissions_tool"`
	AutoReviewSuffix       *codexWireString                   `json:"auto_review_suffix"`
	Granular               *codexWireGranularApprovalMessages `json:"granular"`
}

type codexWireGranularApprovalMessages struct {
	Intro              *codexWireString `json:"intro"`
	PromptedCategories *codexWireString `json:"prompted_categories"`
	RejectedCategories *codexWireString `json:"rejected_categories"`
}

type codexWireCollaborationModeMessages struct {
	Default *codexWireString `json:"default"`
	Plan    *codexWireString `json:"plan"`
}

type codexWireAutoReviewMessages struct {
	Policy                *codexWireString `json:"policy"`
	PolicyTemplate        *codexWireString `json:"policy_template"`
	NodeReplPolicy        *codexWireString `json:"node_repl_policy"`
	RejectionInstructions *codexWireString `json:"rejection_instructions"`
	TimeoutInstructions   *codexWireString `json:"timeout_instructions"`
}

type codexWirePermissionMessages struct {
	DangerFullAccess           *codexWireString `json:"danger_full_access"`
	WorkspaceWrite             *codexWireString `json:"workspace_write"`
	ReadOnly                   *codexWireString `json:"read_only"`
	ApprovedPrefixes           *codexWireString `json:"approved_prefixes"`
	ApprovedCommandPrefixSaved *codexWireString `json:"approved_command_prefix_saved"`
	WritableRoot               *codexWireString `json:"writable_root"`
	WritableRoots              *codexWireString `json:"writable_roots"`
	OmittedPaths               *codexWireString `json:"omitted_paths"`
	DeniedReads                *codexWireString `json:"denied_reads"`
}

type codexWireMultiAgentMessages struct {
	Role *codexWireMultiAgentRoleMessages `json:"role"`
	Mode *codexWireMultiAgentModeMessages `json:"mode"`
}

type codexWireMultiAgentRoleMessages struct {
	Root     *codexWireString `json:"root"`
	Subagent *codexWireString `json:"subagent"`
}

type codexWireMultiAgentModeMessages struct {
	Explicit  *codexWireString `json:"explicit"`
	Proactive *codexWireString `json:"proactive"`
	HintText  *codexWireString `json:"hint_text"`
}

type codexWireModelTokenBudgetConfig struct {
	Enabled                         codexWireBool    `json:"enabled"`
	UseHistoryNotesExtension        codexWireBool    `json:"use_history_notes_extension"`
	ReminderThresholdTokens         *codexWireInt    `json:"reminder_threshold_tokens"`
	ReminderMessageTemplate         *codexWireString `json:"reminder_message_template"`
	GuidanceMessage                 *codexWireString `json:"guidance_message"`
	AutoCompactFallbackPrompt       *codexWireString `json:"auto_compact_fallback_prompt"`
	AutoCompactFallbackBufferTokens *codexWireInt    `json:"auto_compact_fallback_buffer_tokens"`
}

type codexWireGuardianV2ModelConfig struct {
	AsyncClassifierMode                   *codexWireClassifierMode                  `json:"async_classifier_mode"`
	AsyncClassifierConversationTokenLimit *uint64                                   `json:"async_classifier_conversation_token_limit"`
	ClassifierInstructions                *codexWireString                          `json:"classifier_instructions"`
	ReviewThresholdBasisPoints            *uint16                                   `json:"review_threshold_basis_points"`
	MaxToolCallLag                        *uint64                                   `json:"max_tool_call_lag"`
	ReasoningEffort                       *codexWireString                          `json:"reasoning_effort"`
	Transcript                            *codexWireGuardianV2TranscriptModelConfig `json:"transcript"`
	MaxActionTokens                       *uint64                                   `json:"max_action_tokens"`
	MaxClassifierInstructionTokens        *uint64                                   `json:"max_classifier_instruction_tokens"`
	ReuseParentCompaction                 *codexWireBool                            `json:"reuse_parent_compaction"`
	MaxParentCompactionTokens             *uint64                                   `json:"max_parent_compaction_tokens"`
}

type codexWireGuardianV2TranscriptModelConfig struct {
	Sources                    *[]codexWireString `json:"sources"`
	IncludeImages              *codexWireBool     `json:"include_images"`
	MaxMessageEntryTokens      *uint64            `json:"max_message_entry_tokens"`
	MaxToolEntryTokens         *uint64            `json:"max_tool_entry_tokens"`
	MaxMessageTranscriptTokens *uint64            `json:"max_message_transcript_tokens"`
	MaxToolTranscriptTokens    *uint64            `json:"max_tool_transcript_tokens"`
	MaxRecentNonUserEntries    *uint64            `json:"max_recent_non_user_entries"`
}

type codexWireConfirmationPolicies struct {
	BrowserUse  *codexWireString `json:"browser_use"`
	ComputerUse *codexWireString `json:"computer_use"`
}

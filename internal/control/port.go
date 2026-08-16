package control

import (
	"context"

	"corvus/internal/billing"
	"corvus/internal/command"
	"corvus/internal/config"
	"corvus/internal/evidence"
	"corvus/internal/hook"
	"corvus/internal/jobs"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/skill"
)

// This file declares the narrow control surfaces the cli consumes as
// interfaces (MCP management and status reads); everything else in the cli
// drives the concrete *Controller directly.

// Capabilities covers the session's pluggable surface — MCP servers, skills,
// slash commands, hooks — and resolving prompt/command/skill inputs.
type Capabilities interface {
	Host() *plugin.Host
	Commands() []command.Command
	ReloadCommands(ctx context.Context) error
	Skills() []skill.Skill
	SlashSkills() []skill.Skill
	AllSkills() []skill.Skill
	DisabledSkills() []skill.Skill
	SkillEnabled(name string) bool
	SetSkillEnabled(name string, enabled bool) error
	CreateSkill(name string, scope skill.Scope, content string) (string, error)
	UpdateSkill(name string, scope skill.Scope, content string) error
	DeleteSkill(name string, scope skill.Scope) error
	HookRunner() *hook.Runner
	CustomCommand(input string) (sent string, found bool)
	MCPPrompt(ctx context.Context, input string) (sent string, found bool, err error)
	RunSkill(input string) (sent string, found bool)
	AddMCPServer(e config.PluginEntry) (int, error)
	RegisterMCPServerOnDemand(e config.PluginEntry) (int, error)
	ConnectConfiguredMCPServer(name string) (int, error)
	DisconnectMCPServer(name string) bool
	RemoveMCPServer(name string) (disconnected bool, err error)
	ConfiguredMCPNames() []string
	DisconnectedMCPNames() []string
	ImportMCPEntries(entries []config.PluginEntry) (total, added, updated, connected, failed, skipped int, err error)
}

// Status covers read-only run/usage/billing telemetry and task list state.
type Status interface {
	ContextSnapshot() (int, int)
	LastUsage() *provider.Usage
	Balance(ctx context.Context) (*billing.Balance, error)
	Jobs() []jobs.View
	Todos() []evidence.TodoItem
}

// Compile-time proof that the concrete controller satisfies the declared ports.
var (
	_ Capabilities = (*Controller)(nil)
	_ Status       = (*Controller)(nil)
)

# ADR 0010: Project Hook Network Isolation

- **Date**: 2026-08-18
- **Status**: Accepted
- **Context**: Security hardening for repository-controlled lifecycle hooks

## Context

Project hooks (`.corvus/settings.json`) are repository-controlled shell scripts that execute on lifecycle events (PreToolUse, PostToolUse, Stop, etc.). They represent the least-trusted code path in Corvus because:

1. They are cloned with the repository
2. They run with workspace write access after trust approval
3. They execute before the user reviews individual commands

Commit b624b87 established the trust gate and OS sandbox framework for project hooks. However, the network isolation was incomplete: project hooks inherited `cfg.Sandbox.Network` from the bash tool configuration, which defaults to `true` for package manager compatibility.

### Threat Model

Even after a user trusts a project, a malicious or compromised project hook with network access can:

- **Exfiltrate credentials**: Read `~/.aws/credentials`, `.env` files (if not protected), or environment variables that survived `FilterEnv`, then POST them to an attacker-controlled endpoint
- **Download second-stage payloads**: Fetch malicious binaries or scripts from remote servers
- **Command and control**: Beacon to external infrastructure for instructions
- **Supply chain attacks**: Modify dependencies or inject backdoors during the trust window

### Defense-in-Depth Rationale

Corvus's security model uses layered defenses:

1. **Trust gate**: User must explicitly approve project hooks (interactive prompt or remembered grant)
2. **Environment scrubbing**: `secrets.FilterEnv()` removes credential-like variables
3. **OS sandbox**: bubblewrap/Seatbelt confines filesystem writes to workspace + allowed roots
4. **Network isolation**: ← **This layer was incomplete**

The bash tool needs network access for legitimate workflows (npm install, go get, cargo build). Project hooks do not: they are lifecycle observers and local automation scripts with no valid reason to reach external networks.

## Decision

**Project hooks are hardcoded to `Network: false` in their sandbox spec, independent of the bash tool's network configuration.**

Implementation:
- `internal/boot/boot_hooks.go`: `hookProjectSandbox()` sets `Network: false` explicitly
- Comment clarifies the rationale: "repository-controlled lifecycle scripts with no legitimate need for network access"
- Test validates isolation: `TestProjectHookSandboxBlocksNetworkEgress` confirms sandboxed hooks cannot reach external IPs

This applies only to project-scoped hooks. Global hooks (`~/.corvus/settings.json`) and plugin hooks remain unsandboxed because they are user-installed configuration, not repository-controlled code.

## Consequences

### Security

✅ **Eliminates the exfiltration vector**: Trusted project hooks can no longer send data to remote servers
✅ **Prevents remote code execution**: Hooks cannot fetch and execute attacker-supplied payloads
✅ **Fail-closed**: When OS sandbox backend is unavailable, hooks don't run (existing behavior preserved)

### Compatibility

✅ **No breaking changes for legitimate hooks**: Valid use cases (logging, file transforms, local notifications) do not require network
✅ **Clear failure mode**: A hook attempting network access fails immediately with ENETUNREACH/timeout rather than silently succeeding

### Edge Cases

- **Webhook notifications**: If users previously configured project hooks to POST to Slack/Discord, those hooks will now fail. Workaround: move webhook logic to global hooks or use a skill/MCP tool that the model invokes explicitly.
- **Remote logging**: Similarly, hooks sending telemetry to external collectors must migrate to global scope.

These are intentional: repository-controlled code should not have ambient network access. Notifications and telemetry belong in user-controlled global hooks or explicit tool calls.

## Alternatives Considered

### 1. Make network configurable per-hook

```toml
[[hooks.PreToolUse]]
command = "my-hook.sh"
allow_network = true  # opt-in per hook
```

**Rejected**: A compromised repository can set this flag, nullifying the defense. The flag would only protect against accidental network usage, not malicious intent.

### 2. Inherit bash network setting

**Rejected** (status quo ante): This was the incomplete state. Bash needs network for builds; hooks do not. Conflating the two creates a false choice between "usable bash" and "secure hooks."

### 3. Prompt for network on first use

**Rejected**: Adds approval fatigue for a capability hooks should never need. Also creates a window where the first network request succeeds before the user responds.

### 4. No sandbox, rely on trust gate alone

**Rejected**: Trust is not a binary state. A user may trust a project's maintainers but not every contributor or dependency that could modify `.corvus/settings.json`. Defense-in-depth demands confinement even after trust.

## Implementation Notes

The change is minimal:

```go
// Before
Network: cfg.Sandbox.Network,

// After
Network: false, // hardcoded: project hooks never get network access
```

Tests confirm:
- `TestProjectHookSandboxBlocksNetworkEgress`: Validates isolation (ping fails)
- `TestProjectHookSandboxFailsClosedWhenBackendUnavailable`: Confirms fail-closed (hook refuses to run unconfined)
- `TestGlobalHookNotSandboxed`: Ensures global hooks remain unsandboxed

## References

- Commit b624b87: "close trust-boundary gaps and harden reliability from 2nd audit"
- Design review: `docs/notes/2026-08-17-agent-harness-design-review.md` § "Codex: workspace-write network default closed"
- Codex reference: workspace-write sandbox defaults to no network for similar threat model
- Package: `internal/sandbox` — bubblewrap `--unshare-net` / Seatbelt network denial

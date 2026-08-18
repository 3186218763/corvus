# Corvus Runtime Policy Context

This repository uses **runtime policy** to describe how a model turn is guided,
what completion evidence is required, and which tools are initially visible.
These are independent host decisions, not one scalar quality or permission
setting.

Canonical terms:

- **Guidance** is model-visible cognitive scaffolding: `off`, `light`, or
  `structured`.
- **Completion** is the host's acceptance contract: `standard` or `verified`.
  It controls evidence and finalization behavior, not human approval.
- **Exposure** is the initial tool surface: `eager` or `deferred`.
- **Permission** is the existing allow/ask/deny and approval policy. It is
  independent from completion and exposure.
- **Planner** is the existing optional planning model selected by
  `agent.planner_model`; it is not enabled by completion policy.
- **Capability tier** describes the model's inherent capacity (`strong`,
  `standard`, `lite`) and comes from explicit configuration metadata.

The compatibility input `TokenMode` remains a public preset adapter while the
runtime-policy resolver becomes the semantic owner. The authoritative design
and execution instructions are:

1. `.scratch/RUNTIME_POLICY_SPEC_V5.md`
2. `docs/adr/0012-runtime-policy-separation.md`
3. `.scratch/IMPLEMENTATION_PLAN_RUNTIME_POLICY_V5.md`

Do not infer capability from model IDs, use completion as a permission proxy,
or add scheduler policy by matching tool names.

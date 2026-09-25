---
name: requirments
description: Identify and trace FlowAI feature requirements to current contracts, planned OpenSpec changes, verification, and the relevant project skills. Use when asked to gather, review, or inventory requirements for this repository.
---

# FlowAI requirements

Use the repository as the source of truth. Read `AGENTS.md` for service boundaries,
`openspec/specs/` for accepted behavior, and relevant active changes under
`openspec/changes/` for proposed behavior. Read archived changes only for
history; an archive entry by itself does not prove implementation.

For a requested feature or review:

1. State the intended outcome and affected services in plain language. Identify
   each requirement's source and whether it is current, proposed, or missing.
2. Preserve State Registry authority for durable task state and the Web UI →
   API Gateway → State Registry operator path. Keep each Executor and service
   self-contained as specified in `AGENTS.md`.
3. Describe observable acceptance scenarios and the cross-service E2E evidence
   needed for new behavior. When detailed design is deferred, record open
   questions instead of treating guesses as requirements.
4. Use the relevant specialist skill only when its actual task calls for it.
   The [project skill inventory](references/project-skills.md) lists the
   locally linked skills and distinguishes availability from verified use.
5. Put proposed contract changes in a numbered OpenSpec change. Do not edit
   the accepted baseline or architecture diagrams to describe unimplemented
   behavior.

When updating the inventory, inspect the local `.agents/skills/` links and
compare the names against the reference. Record the observation date and
missing targets. These links point outside the repository and are not shipped
with this skill; do not assume a fresh checkout has them installed.

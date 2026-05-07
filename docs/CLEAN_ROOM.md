# Clean Room Rules

This project may study public, permissively licensed projects for ideas, but its
implementation must remain clean-room unless a dependency is intentionally added
with license review.

## Allowed Sources

- DeepSeek-TUI: architecture and product patterns; MIT license.
- RTK: output filtering and command compression patterns; Apache-2.0 license.
- Hermes Agent: long-running worker, gateway, memory, and automation patterns;
  MIT license.
- Xiaomi MiMo docs and model cards: product requirements and provider behavior.

## Restricted Source

The local Claude Code snapshot is for internal architecture study only. Do not
copy source code, prompts, private identifiers, or implementation-specific text
from it into this repository.

## Practical Rule

When in doubt, re-describe the behavior in product terms, write new Go code, and
add tests for our intended behavior.

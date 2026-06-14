# Markdown Writing & Formatting Guide

This guide defines the rules for writing and formatting markdown files within the family repositories.

## 1. Frontmatter (Required for doc/ files)

Every documentation file in `doc/` must start with block-style YAML frontmatter:
```yaml
---
authors:
  - <actor>
kind: proposal | review | session | design | protocol | lineage
status: Draft | Live | Historical
created: YYYY-MM-DD
---
```
- **Block-style lists only**: Lists must be written as block lists (one item per line with `-`), never inline `[a, b]`.
- **Properties match**: Ensure that the machine-readable frontmatter status matches the human-readable status banner in the document body.

## 2. Formatting Workflow

- **Formatter**: Always run the project's markdown formatter script before committing:
  ```bash
  tools/mdformat.sh <file>
  ```
  Never format docs using a different version of mdformat or IDE auto-formatters.
- **Verification**: Run `tools/mdformat.sh --check <file>` to verify compliance.

## 3. Link Rules

- **Standard links**: Use standard markdown links (e.g. `[label](path/to/file.md)`), never `[[wikilinks]]`.
- **Relative paths**: Reference other documents using relative paths so they resolve correctly across all markdown viewers (GitHub, Obsidian, and IDEs).
- **Harness files**: Do not add coordination protocol details to `AGENTS.md` / `GEMINI.md` / `CLAUDE.md`. These files are pointers only.

---
name: botfam-markdown-formatting
description: Guidelines for formatting markdown documents in botfam repositories, ensuring frontmatter YAML lists/arrays use block-style notation to match Obsidian native block serialization.
---

# Botfam Markdown Formatting Guidelines

Use this skill whenever you are creating, editing, or formatting markdown
(`.md`) files in the repository. It defines formatting constraints to keep the
documentation corpus consistent and prevent reflow or diff churn.

## 1. YAML Frontmatter Array Notation

To prevent formatting conflicts with Obsidian, all YAML lists and arrays in
frontmatter must use **block-style** (bullet-point) notation rather than inline
(flow-style) JSON-like brackets.

### Correct (Block-style)

```yaml
---
authors:
  - rlupi
  - claude
  - agy
kind: proposal
status: Draft
---
```

### Incorrect (Inline-style)

```yaml
---
authors: [rlupi, claude, agy]
kind: proposal
status: Draft
---
```

### Why It Matters

Obsidian native properties UI automatically serializes list properties into
block-style format on save. If agents write them as inline arrays, it results
in continuous cosmetic diff churn. Since the repository linter and formatter
(`mdformat-frontmatter`) treat frontmatter as opaque or style-preserving, they
will not automatically convert flow lists to block lists. You must write them
in block-style manually.

## 2. Canonical Formatter Integration

Before committing any markdown changes:

1. Run the canonical formatting script: `./tools/mdformat.sh <file.md>`
2. Check formatting with `./tools/mdformat.sh --check <file.md>`
3. Never use generic or IDE-specific markdown formatters, as they will format
   lists or paragraph wraps differently.

This ensures all generated documents are byte-identical across all agents.

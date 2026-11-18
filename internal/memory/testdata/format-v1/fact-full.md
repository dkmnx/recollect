---
name: xi-memory-format-v1
description: xi project memory stores one fact per markdown file with YAML frontmatter
metadata:
  node_type: memory
  type: project
  originSessionId: sess_ca25f4c2-1f09-45ad-9d50-a95615be4fe9
---

xi adopted the per-fact format on 2026-09-23.

**Why:** a generic reader treats every `.md` as one memory, so hand-placed
files can never break the surfaces.

**How to apply:** address facts by file name; see [[memory-index-is-derived]].

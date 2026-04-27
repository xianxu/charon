---
name: xx-memory
description: "Use when the user wants to capture, distill, or organize information from a chat session into a structured, persistent file. Invoked as /xx-memory <topic-slug>, or automatically when the user says 'remember this', 'capture this as memory', 'let's track X', or asks to maintain a file/list of something. Sister skill to /xx-pensive — pensive is for trains of thought, memory is for structured artifacts the user will revisit and update."
---

# Memory

Capture chat content into a structured memory file under `memory/` (or a user-specified base folder).

Memory is for things the user will come back to and update: travel plans, project setups, reusable task recipes, maintained lists. Contrast with `/xx-pensive`, which captures unstructured thinking-out-loud.

## Usage

```
/xx-memory <topic-slug>
```

Or invoked automatically when the user says "remember this", "capture this as memory", "let's track X", or asks to maintain a file/list of something across sessions.

## Memory Types

Every memory file declares one type in front matter:

| Type | Use for | Examples |
|---|---|---|
| `memory.data` | Evergreen, non-time-sensitive information | Home contractors list, vendor reference, recurring people |
| `memory.task` | Steps to follow for a repeatable or in-flight procedure | "Steps to create an Apple Developer ID", company formation checklist |
| `memory.event` | Time-sensitive plans bound to a due date | Trip itinerary, conference prep, launch plan |

## Process

1. **Determine the base folder.** Default is `memory/`. Use that unless the user specified otherwise.
2. **Discover existing structure.** Run `find <base> -type d` to see what subdirectories already exist.
3. **Pick a location.**
   - If a clearly fitting subdirectory exists, place the file there.
   - If the topic is new but adjacent, create a subdirectory matching the naming scheme already in use.
   - If unsure, ask the user before writing.
4. **Pick a filename.** Kebab-case slug with `.md` extension.
5. **Write the file:**
   ```markdown
   ---
   type: memory.data | memory.task | memory.event
   ---

   # <Title>

   <Body — see conventions below>
   ```
6. **Tell the user the path** in your response so they can see where it landed.

## Body Conventions

Shape the body to fit the type. Not rigid — adjust to content — but follow the spirit:

- **`memory.data`** — Evergreen reference. Lists or tables. Optionally include a `Last reviewed:` line if the data can go stale.
- **`memory.task`** — Lead with the goal. Numbered steps, checkable where helpful. Prerequisites and gotchas inline. Make parameterized placeholders obvious.
- **`memory.event`** — Lead with the date and a one-line summary. Section per phase (before / during / after) or per day. Surface unresolved blockers and decisions still to make.

## Updating an Existing Memory

When the user adds to an existing memory, edit in place rather than creating a new file. Bump a `Last updated:` line if one is present.

## Rules

- Capture the substance, not the chat. Rewrite into structured prose, not a transcript.
- Keep the user's voice and naming. Don't over-formalize a casual list.
- For tasks and events, surface unresolved decisions and TODOs explicitly — don't bury them.
- Short is fine. Memory files should be useful at a glance.
- Don't create subdirectories speculatively — only when a second file would actually belong there.

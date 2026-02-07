# `codex-trace` SPECS

---

Build an MVP TUI app (macOS first, Linux supported; Windows not required) that browses Codex CLI history/sessions and exports a session transcript to Markdown for linking in PRs/issues. Use the Charm stack:

* [https://github.com/charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)
* [https://github.com/charmbracelet/bubbles](https://github.com/charmbracelet/bubbles)
* [https://github.com/charmbracelet/glamour](https://github.com/charmbracelet/glamour)
* (also use lipgloss for layout/theming)

### Product goals

1. A full-screen terminal TUI that lists Codex sessions on the left and shows the selected session transcript on the right.
2. Fast global search across all sessions and messages using SQLite with FTS5.
3. One-key export of the selected session transcript to `docs/codex/<session-id>.md` (configurable output path).
4. “Looks awesome”: polished layout, status line, help bar, responsive resizing, and good typography.
5. Works offline; do not call network APIs; only read local files.

### Data sources (important)

Codex stores session “rollout” JSONL event logs under `$CODEX_HOME/sessions/**/rollout-*.jsonl` (typical default `$HOME/.codex`). Users may disable history persistence, so **prefer parsing rollout logs**. If no rollouts exist, fallback to `$CODEX_HOME/history.jsonl` if present.

Implement a robust scanner:

* Determine `CODEX_HOME`:

  * if env `CODEX_HOME` set, use it
  * else default to `$HOME/.codex`
* Prefer sources in this order:

  1. any `sessions/**/rollout-*.jsonl`
  2. else `history.jsonl` (if exists)
* Parse JSONL line-by-line; ignore malformed lines safely.

### Which events to treat as canonical “conversation”

In Codex rollout logs there are multiple event types. Only treat these as canonical transcript messages by default:

* `type == "message"` AND `role in {"user","assistant"}`

Do NOT duplicate `user_message` by default (it often corresponds to the same user turn). However, support an optional toggle to include aborted/extra user inputs via `type=="user_message"` only when no corresponding canonical message exists (best-effort).

Also support optional toggles for:

* Include tool calls / tool output if present in events (best-effort)
* Include “non-message” events with human-readable content (best-effort)
  These toggles should be OFF by default.

### MVP UI requirements (Bubble Tea + Bubbles + Glamour + Lipgloss)

Layout:

* Left pane: sessions list (Bubbles `list.Model`)
* Right pane: transcript viewer (Bubbles `viewport.Model`)
* Top status line: shows:

  * Selected session id (shortened)
  * Turn/message count
  * Last activity timestamp
  * Current source (rollout vs history)
  * Search mode indicator if active
* Bottom help bar: Bubbles `help.Model` with keybindings

Session list item display:

* Title: repo/workdir if detectable (otherwise session id)
* Subtitle: “last active” timestamp + message count
* Description: preview snippet (first user message or last user message)

Transcript viewer:

* Render as Markdown via Glamour for a nice look.
* Use a stable glamour style (do NOT rely purely on auto mode) because Glamour can have odd interactions in some interactive TUIs (rendering glitches / style issues). Provide a config or constant for a known good glamour style and keep rendering isolated to the viewport content string.

Important Glamour gotcha handling:

* Avoid frequently re-rendering the entire transcript on every keypress.
* Cache rendered markdown per session and only re-render when:

  * selected session changes
  * toggles change
  * terminal width changes significantly (wrap)
* Keep Glamour rendering output as plain string set into viewport; don’t mix direct terminal writes.

Keybindings (MVP):

* Up/Down or j/k: move selection in sessions list
* Enter: focus transcript pane (optional) / or just keep single focus but allow scrolling transcript with PgUp/PgDn
* Tab: toggle focus between session list and transcript viewport (so scrolling works naturally)
* / : open search mode (text input)
* Esc: exit search mode / clear search query
* n / p: next/previous search result inside transcript (optional if easy)
* e: export selected session to markdown (with current toggles applied)
* t: toggle include tool calls/output
* a: toggle include aborted inputs (`user_message` fallback)
* q: quit

Search UX:

* Press `/` to bring up a text input (Bubbles `textinput.Model`) in the status line or a small overlay.
* Searching should query SQLite FTS across message text and rank sessions by relevance.
* Left pane should update to show filtered sessions ranked by match.
* When selecting a session from search results, transcript pane shows full transcript; highlight matches if feasible (optional).

### SQLite data model (Option B, required)

Use SQLite database file stored at:

* `$CODEX_HOME/codex-history-index.sqlite` by default (or in `$XDG_CACHE_HOME` if you think better on Linux; but keep it simple and consistent).

Tables:

1. sessions

* id TEXT PRIMARY KEY  (session_id)
* source TEXT          ("rollout" or "history")
* last_activity_ts INTEGER (unix epoch)
* message_count INTEGER
* workdir TEXT NULL
* preview TEXT NULL

2. messages

* id INTEGER PRIMARY KEY AUTOINCREMENT
* session_id TEXT (FK)
* ts INTEGER NULL
* role TEXT  ("user"|"assistant")
* content TEXT
* type TEXT  (original event type e.g. "message" or "user_message")
* raw_json TEXT NULL (optional for debugging; can omit to keep db smaller)

FTS:

* Create `messages_fts` as FTS5 over content (and maybe role) with `content=messages` or standalone.
* Ensure queries can efficiently return top sessions by match:

  * e.g., SELECT session_id, bm25(messages_fts) … GROUP BY session_id ORDER BY score LIMIT …

Indexing pipeline:

* On startup:

  * scan filesystem for sources (rollout JSONL files or history.jsonl)
  * build or incrementally update index
* Incremental updates:

  * track file path + last processed byte offset OR last modified timestamp (choose a pragmatic approach)
  * store ingestion metadata table:

    * ingested_files(path TEXT PRIMARY KEY, mtime INTEGER, size INTEGER, offset INTEGER)
* Must be robust: if files shrink/rotate, fall back to re-ingesting that file.

Parsing heuristics:

* JSON schema may evolve; parse into `map[string]any`.
* For each line:

  * detect `type`
  * if type=="message":

    * role: obj["role"] OR obj["message"]["role"] OR obj["data"]["role"] etc (best-effort)
    * content: obj["content"] OR nested equivalents; content may be string or list of blocks; coerce to text.
  * if type=="user_message":

    * content: similar extraction; store as role="user", type="user_message"
* Timestamp:

  * If present, normalize to unix epoch; else leave NULL.
* Session id:

  * Use explicit session field if present; else infer from directory name if needed.
  * For rollout files, session id might be in the JSON lines and/or in the directory path; implement best-effort extraction.
* Workdir/repo:

  * best effort from event fields if present; if not, attempt to infer repo by walking up from a path field (if any) to find `.git` and reading repo root name (do not run git commands unless necessary; prefer filesystem checks).

### Export functionality (required)

When pressing `e`:

* Export the selected session transcript to Markdown.
* Default path: `docs/codex/<session-id>.md` relative to detected repo root if available; otherwise current working directory; if none, use `$PWD/docs/codex/`.
* Ensure directory exists (mkdir -p).
* Markdown format:

  * Title: `# Codex session <session-id>`
  * Exported timestamp (ISO-8601)
  * A short metadata block:

    * source (rollout/history)
    * message_count
    * repo/workdir if known
  * Then transcript:

    * `## You` sections for user
    * `## Codex` sections for assistant
  * Preserve code blocks as-is (don’t rewrap or alter fenced code).
* Apply toggles:

  * if include tool calls on: include tool call events as `## Tool` sections with fenced code JSON or summarized text.
  * if include aborted inputs on: include orphan `user_message` entries marked as `(aborted)`.

### Project structure & deliverables

Create a Go module with a clean structure, e.g.:

* cmd/codex-trace/main.go
* internal/ui (bubbletea model/update/view)
* internal/index (scanner, parser, sqlite, fts)
* internal/export (markdown export)
* internal/config (CODEX_HOME detection, paths, style constants)
  Include:
* README with install/run instructions
* Makefile or just `go run ./cmd/codex-trace`
* Sensible defaults and flags:

  * `--codex-home`
  * `--db-path`
  * `--reindex` (force rebuild)
  * `--export-dir` (optional override)

### Performance & polish requirements

* Use context cancellation for indexing if possible.
* Don’t block UI while indexing; show a loading indicator/spinner while indexing updates.
* Cache rendered transcript strings by session id + toggles + width to avoid Glamour flicker.
* Handle terminal resize.
* Ensure keybindings are shown in the help bar.
* Use lipgloss for a cohesive theme: borders, padding, subtle emphasis, active focus highlight.

### Acceptance criteria

* I can run it locally.
* It finds sessions from rollout logs under `~/.codex/sessions/**/rollout-*.jsonl`.
* Left pane lists sessions with good preview info.
* Right pane renders a readable transcript with Glamour styling.
* `/` search filters/ranks sessions using SQLite FTS.
* `e` exports Markdown to `docs/codex/<session-id>.md`.
* Toggles `t` and `a` work and change transcript + export output.
* Works on macOS; Linux should work with the same codebase.

Implement the MVP end-to-end with working code, not pseudocode.

---

## IMPORTANT: This is the end of the initial MVP instructions. Ignore the remaining document below this next line until all of the above has been completed. Only proceed if the MVP is working with all requirements met.

---

Enhance the existing Codex History TUI MVP (Bubble Tea + Bubbles + Glamour + Lipgloss + SQLite FTS5) with two polished features:

1. **Search match highlighting in the transcript viewer**
2. **Copy PR snippet action (clipboard integration on macOS + Linux)**

Do not regress MVP behavior. Keep the code clean and modular.

---

# 1) Search match highlighting in transcript

## Goal

When the user searches (`/query`), show matches in two places:

* **Sessions list** already filters/ranks; keep that.
* **Transcript viewer** should visually highlight occurrences of the current search query within the transcript.

## Constraints / gotchas

* Glamour renders Markdown to ANSI styled text. Naively injecting highlight styles into Markdown can conflict with Glamour formatting.
* Implement highlighting in a way that:

  * preserves Glamour formatting as much as possible
  * does not cause flicker
  * avoids re-rendering the transcript on every keystroke more than needed

## Approach (recommended)

* Render transcript via Glamour to ANSI text (as currently done).
* Apply highlighting **after** Glamour rendering by post-processing the ANSI string:

  * Find matches of the search query in the *plain text* portion.
  * Wrap matched substrings with an additional ANSI style (Lipgloss style) that stands out.
* Because ANSI post-processing is tricky:

  * Implement a minimal, safe highlighter that works reasonably well:

    * Case-insensitive substring matching by default.
    * Avoid matching across line breaks.
    * Do not attempt to parse full ANSI sequences; instead, use a conservative algorithm:

      * Split on ANSI escape sequences and only search/replace within non-escape segments.
      * Recombine with escapes untouched.
* Add a toggle to enable/disable highlight if needed (optional).

## UX

* While search query is active:

  * Highlight all matches in transcript.
  * Add a small indicator in status line like: `Matches: <n>` (best-effort count).
* Add navigation:

  * `n` jumps to next match (scroll viewport so match is visible)
  * `p` jumps to previous match
* If jump-to is hard with ANSI text:

  * Implement approximate jump by keeping a list of line indices containing matches in the *unrendered* transcript and scrolling viewport by lines.

---

# 2) Copy PR snippet action

## Goal

Pressing a key should copy a ready-to-paste Markdown snippet to clipboard, referencing the exported transcript file and optionally including a brief summary.

Add keybinding:

* `c`: copy PR snippet to clipboard

## Snippet format

When a session is selected, generate a snippet like:

```md
### Codex transcript

- Session: `<session-id>`
- Export: `docs/codex/<session-id>.md`
- Notes: <short summary or first user line>
```

If the export file does not exist yet:

* Either export automatically first (preferred), or include a note saying it hasn’t been exported.

## Clipboard integration

* macOS: use `pbcopy`
* Linux: prefer `wl-copy` if present; else `xclip -selection clipboard` if present
* If no clipboard tool exists:

  * show a non-fatal error message in the status bar (“clipboard tool not found”)
  * still print the snippet to stdout on quit? (optional)
* Never require any third-party Go clipboard library; just shell out to these commands.
* Make clipboard calls safe:

  * use `exec.Command`
  * write snippet to stdin of pbcopy/wl-copy/xclip
  * time out if needed (optional)

## UX

* On success: show a transient toast/status message: “Copied PR snippet to clipboard”
* On failure: show “Could not copy: <reason>”

---

# Additional polish requirements

* Update help bar to include new keybindings (`c`, `n`, `p`).
* Keep Glamour gotcha handling intact:

  * continue caching rendered transcript per session + toggles + width
  * do not re-render with Glamour on every search keystroke if avoidable
* Add tests where reasonable:

  * unit tests for ANSI-safe highlighting (at least a few cases)
  * unit tests for clipboard command selection logic (without actually invoking clipboard)

---

# Acceptance criteria

* Searching highlights matches in transcript.
* `n` / `p` navigates between matches (even if approximate, must be useful).
* `c` copies a valid markdown snippet to clipboard on macOS; on Linux works if `wl-copy` or `xclip` is installed.
* No UI flicker or major performance regression.
* Code remains clean, with new functionality in clearly named packages/modules (e.g., `internal/highlight`, `internal/clipboard`).

Implement with working code changes, not pseudocode.

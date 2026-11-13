---
name: peek
description: Look at a data file or a log with `peek FILE` instead of cat, head, tail, `grep -c`, `cut -c`, or inline Python/jq that prints a JSON file's keys. For JSON it prints every path once with its type and values (arrays merged), so you learn the shape in one call, then `peek FILE '.path[0].x'` shows one part; JSON Lines (transcripts, events) are streamed and merged the same way; CSV/TSV get their columns; logs and command output get their errors and warnings grouped, repeated lines collapsed, long lines clipped and the last lines. `peek FILE -g PATTERN` greps a log grouped by template, `peek FILE 100-200` prints lines. Works on stdin too (`gh api ... | peek`). Use it whenever you would read a data file or log you haven't seen.
---

# peek for agents

`peek FILE` prints a bounded card (about 60 lines, `-b N` to change) about what is in a file. Use it before reading a JSON file, a JSON Lines file, a CSV, or a log you don't know yet.

## JSON

```
$ peek data.json
peek: data.json  JSON, 374 B, object with 3 keys
.name                   string                   "aero-run"
.levels                 array of 3 objects
.levels[].id            string                   "level_01", "level_02", "level_03"
.levels[].beats[].kind  string                   "tap" ×4, "hold" ×1, "slide" ×1
.levels[].critic        object                   in 1 of 3
...
next: peek data.json '.levels[0]'   the first element in full
```

Every path appears once, arrays merged (`[]` means "every element"). `in 1 of 3` means only some elements have the field. Then look at one part with a jq path, **quoted** (the shell treats `[` specially):

| Want | Command |
| --- | --- |
| One element in full, as JSON | `peek data.json '.levels[0]'` |
| The last one | `peek data.json '.levels[-1]'` |
| One field of every element | `peek data.json '.levels[].id'` (listed when few, counted when many) |
| A key with spaces or dashes | `peek data.json '.["a key"]'` |
| The whole document | `peek data.json .` (its shape if too long) |

Long strings are clipped and long arrays cut, with a note. A path that selects nothing says where it stopped and lists the keys that are there.

Command output works the same way: `gh api repos/o/r/pulls | peek`, `gh pr view 12 --json files | peek '.files[].path'`.

## JSON Lines

`peek events.jsonl` streams the records (any size, little memory) and shows the merged shape with counts (`.type  "assistant" ×5,136, "user" ×3,317`). A path applies to every record: `peek t.jsonl '.message.content[].type'`.

## CSV / TSV

Columns with type, range or counts and empty cells, then the first rows aligned.

## Logs and other text

```
$ peek run.log
peek: run.log  8,248 lines, 279 KB; 3 errors, 41 warnings
errors (3, 2 kinds)
  L1210  ERROR: Node not found: "Player/Camera"   ×2, also L1460
  L5031  SCRIPT ERROR: Invalid call. Nonexistent function jump in base Nil.
warnings (41, 1 kind)
  L4     WARNING: ObjectDB instances leaked at exit   ×41, also L205, L406, L607, … L8047
most repeated
  ×8,200  L3-8246      W <n> tick=<n> pos=(<n>, <n>)
first lines ...
last lines ...
next: peek run.log 1205-1225   the lines around the first error
```

Lines are grouped by template (numbers and IDs as `<n>`), so repeated lines collapse into one with a count. Errors and warnings go by the level the line starts with or the logger wrote (`error:`, `ERROR`, `[main/WARN]`, `level=info`) or else by words and names like `failed`, `FAIL`, `ValueError`, `Caused by: …Exception`, `warning`; passing test lines (`--- PASS`, `ok  pkg`) never count. Long lines show their start and end (the end often has the reason).

| Want | Command |
| --- | --- |
| The overview | `peek run.log` |
| Lines matching a pattern, grouped | `peek run.log -g 'RUN \d+'` (`-i` ignores case) |
| Some lines, clipped | `peek run.log 1205-1225` |
| The last lines | `peek run.log -t 30` |

A short text is shown whole. For a build or test run, run it through `crux` instead; peek is for logs and outputs that already exist (a game log, a saved trace, a task's output file).

## Other

- `--as json|jsonl|csv|tsv|text` when the extension misleads (`-g` and `-t` only work on text).
- With `-b 0` nothing is clipped: whole values, whole strings.
- A binary file is named (PNG, gzip, ELF, …), not printed.
- Exit code 1 when a path, pattern or line range matched nothing; 2 for bad input.

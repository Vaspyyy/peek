<div align="center">

# peek

A short card about a data file or a log, instead of a screenful of it.

[![Go](https://img.shields.io/github/go-mod/go-version/Vaspyyy/peek?style=flat-square&labelColor=1e1e2e&color=89b4fa&logo=go&logoColor=89b4fa)](go.mod)
[![Release](https://img.shields.io/github/v/tag/Vaspyyy/peek?style=flat-square&labelColor=1e1e2e&color=cba6f7&label=release)](https://github.com/Vaspyyy/peek/tags)
[![License](https://img.shields.io/github/license/Vaspyyy/peek?style=flat-square&labelColor=1e1e2e&color=a6e3a1)](LICENSE)

</div>

peek shows what is in a file without printing all of it: every path of a JSON document with its type and values, the fields of JSON Lines records, the columns of a CSV file, or the errors, repeated lines and last lines of a log. A jq path drills into JSON; a pattern or a line range narrows a log.

It was written for AI coding agents, which pay for every line they read. Before peek they learned a JSON file's shape with `python3 -c "import json; print(json.load(open(p)).keys())"` and then printed one part with `json.dumps(...)[:4000]`, and read logs with `grep -c`, `grep -iE "error|warn"`, `cut -c1-300`, `head` and `tail`. peek does each of those in one command with a bounded answer. It is part of the same family as [glint](https://github.com/Vaspyyy/glint), [seek](https://github.com/Vaspyyy/seek), [recap](https://github.com/Vaspyyy/recap), [crux](https://github.com/Vaspyyy/crux) and [splice](https://github.com/Vaspyyy/splice).

## Install

```sh
go install github.com/Vaspyyy/peek@latest
```

You'll need Go 1.26 or newer (older versions fetch the right toolchain on their own). peek is a single binary with no other dependencies. It's developed on Linux and should work on macOS and the BSDs.

## Usage

```sh
peek [flags] FILE [PATH | LINES]
cmd | peek [flags] [PATH]
```

### JSON

```
$ peek data.json
peek: data.json  JSON, 374 B, object with 3 keys
.name                   string                   "aero-run"
.levels                 array of 3 objects
.levels[].id            string                   "level_01", "level_02", "level_03"
.levels[].beats         array of 1 to 3 objects
.levels[].beats[].t     number                   0.5 to 212
.levels[].beats[].kind  string                   "tap" ×4, "hold" ×1, "slide" ×1
.levels[].critic        object                   in 1 of 3
.levels[].critic.score  number                   7
.settings               object
.settings.volume        number                   0.8
.settings.lang          null
next: peek data.json '.levels[0]'   the first element in full
```

Every path appears once. Arrays are merged, so `.levels[].id` covers the `id` of every element, and a field that only some elements have says how many (`in 1 of 3`). Strings show their value, the counts of a few repeated values, or the number of distinct values with examples; numbers show their range; booleans their counts. An object with more than 40 keys is read as a map (IDs to records): it shows how many keys it has with examples, and its values merged under `.[]`, as in jq.

The paths are jq paths, so any printed path can be given back (quote it, since `[` is special to the shell):

```
$ peek data.json '.levels[1]'
peek: data.json .levels[1]  object with 3 keys
{
  "id": "level_02",
  "beats": [
    {
      "t": 1,
      "kind": "tap"
    }
  ],
  "critic": {
    "score": 7
  }
}
```

`.a.b`, `.a[0]`, `.a[-1]`, `.a[]` (every element, or every value of an object), `.["a key"]` and `."a key"` work. One value is printed as JSON, with strings over 200 characters clipped and long arrays and objects cut; a value too long for the budget gets its shape card instead. Several values (through `[]`) are listed one per line when they are few scalars, and summarized otherwise. A path that selects nothing says where it stopped (`no key "levles" in .; its keys: name, levels, settings`) and exits with 1. A file that isn't valid JSON gets the line and column of the problem.

### JSON Lines

Records are read one at a time, so a file of any size works with little memory (an 884 MB file of 22,714 records takes a few seconds). The card is the merged shape of all records, and a path applies to every record (`peek events.jsonl .message.content[].type`). Lines that aren't JSON are counted, with the first one's number.

### CSV and TSV

The columns with their type and values (empty cells are counted), then the first rows, aligned. A first row with no numbers and no empty cells is taken as the header. Commas, tabs, semicolons and pipes are recognized; rows with another number of fields are counted.

### Text and logs

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
first lines
     1  Engine v4.7.2 starting
     2  loading level_02
     3  W 5.5 tick=0 pos=(9.52, -4.90)
last lines
  8239  W 5.5 tick=8192 pos=(13.11, -2.10)
  …
  8247  RUN 12 done in 4.2 s
  8248  REGRESS EXIT 1
next: peek run.log 1205-1225   the lines around the first error
```

Lines are grouped by template: the line with its numbers (and their signs), hex values and IDs replaced by `<n>`, and runs of spaces as one. That is what turns 8,200 progress lines into one.

A line is an error or a warning by the level its logger wrote (`ERROR`, `FATAL`, `[main/WARN]`, `level=INFO`, …) when it has one, and otherwise by its words: `error`, `fatal`, `panic`, `exception`, `traceback`, `fail`, `failed`, `failure`, `segfault` or a line starting with `not ok` for errors, `warn` or `warning` for warnings, but not in `0 errors`, `no warnings` or `errors: 0`.

Long lines keep their start and their end with the middle cut (`…(1,505 chars)…`), since the end of an error line often says why. A text that fits the budget is shown whole.

- `-g PATTERN`: the lines matching a regular expression (Go syntax; `-i` ignores case), grouped by template the same way. Exit code 1 if nothing matches.
- `LINES`: `peek run.log 1200-1240` prints those lines, `-t N` the last N, clipped.

### Anything else

A file is read by its extension (`.json`, `.jsonl`, `.ndjson`, `.csv`, `.tsv`, `.log`, `.txt`, …), else by its content. `--as json|jsonl|csv|tsv|text` overrides that. A binary file (NUL bytes, or the first bytes of PNG, JPEG, gzip, zip, ELF, PDF, SQLite and a few more) is named, not printed. Standard input works for any kind: `gh api repos/o/r/pulls | peek`, `| peek '.[].title'`.

### Flags

| Flag | |
| --- | --- |
| `-b N` | about how many lines to print (default 60; 0: no limit) |
| `-g PATTERN` | text: the lines matching a regular expression |
| `-i` | with `-g`: ignore case |
| `-t N` | text: the last N lines |
| `--as KIND` | read the input as json, jsonl, csv, tsv or text |
| `-A` | plain output (peek's output is always plain for now) |
| `-h`, `--help` | help |
| `-V`, `--version` | version |

What doesn't fit the budget is counted, not dropped silently: `… 12 more paths deeper`, `… 3 more kinds`. Exit code: 0 shown, 1 a path, pattern or line range matched nothing, 2 bad input.

## For coding agents

`skill/SKILL.md` is an agent skill that says when to use peek and how. Link the folder into your agent's skills folder, for Claude Code `ln -s "$PWD/skill" ~/.claude/skills/peek`.

The design, with the transcript numbers behind it, is in [docs/design.md](docs/design.md).

## License

GPL-3.0, see [LICENSE](LICENSE).

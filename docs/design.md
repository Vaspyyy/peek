# peek: design

peek prints a short card about a data file or a log: the shape of a JSON
document, the fields of JSON Lines records, the columns of a CSV file, or the
errors, repeated lines and last lines of a log. A path drills into JSON; a
pattern or a line range narrows a log.

## Why

In the agent transcripts of 2026-10-14 to 2026-11-13 (about 49,000 shell
commands), agents:

- read `.log`, `.out`, `.err`, `.txt` and task `.output` files raw about
  4,300 times, mostly with `grep -c`, `grep -iE "error|warn"`, `cut -c1-300`
  against long lines, `head` and `tail`;
- parsed JSON with inline Python 1,552 times and with `jq` 1,031 times. About
  1,000 of those were JSON files and transcripts, typically:

  ```python
  d = json.load(open("tests/_out/review/triage/level_03.json"))
  print(d.keys())                                   # what's in it?
  print(json.dumps(d["critic_context"], indent=1)[:4000])  # then one part
  ```

  Most of the rest was `gh` output, which `gh --jq` already covers.

Both are the same move: learn the shape first, then look at one part, and
don't flood the context while doing it. peek makes that one command each.

## Usage

```
peek [flags] FILE [PATH | LINES]
cmd | peek [flags] [PATH]
```

`-` or no file reads standard input (when it isn't a terminal).

### Kinds

By extension first (`.json`, `.jsonl`/`.ndjson`, `.csv`, `.tsv`), then by
content: a document that parses as JSON is JSON; lines that are each a JSON
value are JSON Lines; rows with the same number of commas, tabs or semicolons
are CSV; everything else is text. A file with NUL bytes is binary: peek says
so (with the type its first bytes show: PNG, gzip, zip, ELF, …) and stops.

## JSON

One line per path in the document, with its type and what its values look
like. Arrays are merged: `.levels[].id` covers the `id` of every element.

```
peek: data.json  JSON, 48 KB, object with 4 keys
.name                  string  "aero-run"
.levels                array of 12 objects
.levels[].id           string  12 distinct, like "level_01", "level_02"
.levels[].beats        array of 3 to 40 objects
.levels[].beats[].t    number  0.5 to 212
.levels[].beats[].kind string  "tap" ×310, "hold" ×52, "slide" ×12, "flick" ×3
.levels[].critic       object  in 4 of 12
.settings.volume       number  0.8
next: peek data.json .levels[0]   the first element in full
```

- Paths are jq paths, so a printed path is valid input: `peek data.json
  .levels[].beats[].kind`.
- A field missing from some elements says `in 4 of 12`. A field with several
  types lists them (`string or null`).
- Strings: one value is shown as is (clipped); a few repeated values are
  counted (`"tap" ×310`); many distinct values show the count and two
  examples. Numbers show their range; booleans their counts.
- An object with many keys whose values have one shape (a map from IDs to
  records) is shown as a map: `.items  object with 500 keys like "a12",
  "b7"`, then `.items[]...` for the values, as jq's `.[]` iterates them.
- Within the budget, deeper paths go first: the card shows as many levels as
  fit and says how to see the rest.

### A path

`peek FILE PATH` shows what the path selects. `.a.b`, `.a[0]`, `.a[-1]`,
`.a[]` (every element, or every value of an object) and `.["a key"]` work,
as in jq.

- One value that fits the budget: printed as indented JSON, with long
  strings clipped (`… +3,200 chars`) and long arrays cut (`… 90 more`).
- One value too big for that: its shape card.
- Several values (through `[]`): scalars are counted (`"tap" ×310, …`) and
  listed when few; objects and arrays get the merged shape card.
- A path that selects nothing says where it stopped (`.levels[0].critc: no key
  "critc" in .levels[0]; keys: id, beats, critic`), exit code 1.

## JSON Lines

The records are read one at a time, so a file of any size works. The card is
the merged shape of all records (`.message.content[].type  string
"text" ×9,120, "tool_use" ×4,811, …`), with the record count. A path applies
to every record.

## CSV and TSV

Columns with their type, range or counts, empty cells, then the first rows,
aligned and clipped. A first row that is all text above rows with numbers is
the header.

## Text and logs

```
peek: run.log  12,345 lines, 1.2 MB; 3 errors, 41 warnings
errors
  L1203  ERROR: Node not found: "Player/Camera"   ×2, also L1450
  L9001  SCRIPT ERROR: Invalid call. Nonexistent function 'jump' in base 'Nil'.
warnings (41, 2 kinds)
  L88    WARNING: ObjectDB instances leaked at exit   ×38
  L91    WARNING: 3 RIDs of type "Texture" were leaked.   ×3
most repeated
  ×8,200  L10-9300  W 5.5 tick=<n> pos=(<n>, <n>)
first lines
  1  Godot Engine v4.3.stable.official
last lines
  12341  RUN 12 done in 4.2 s
  12345  REGRESS EXIT 0
```

- Lines are grouped by their **template**: the line with numbers, hex
  values and IDs replaced by `<n>`. A group shows its first line, its count
  and a few more line numbers. That is what collapses a thousand progress
  lines into one.
- **Errors**: lines with `error`, `fatal`, `panic`, `exception`,
  `traceback`, `failed` or `failure` as a word (not "0 errors" or "no
  errors"). **Warnings**: `warn` or `warning`.
- Long lines are clipped (`… +1,840 chars`).
- `-g PATTERN`: the lines matching a regular expression, grouped by template
  the same way, with the total. Exit code 1 if nothing matches.
- `LINES`: `100-200` prints those lines, `-t N` the last N, clipped.

## Output

- A budget of about 60 lines (`-b N`, `-b 0` for no limit). What doesn't fit
  is counted, never silently dropped, and a `next:` line says how to see it.
- Plain text when piped or with `-A`, colors on a terminal, like the other
  tools.
- Exit codes: 0 shown, 1 a path or pattern matched nothing, 2 bad input.

## Not doing (for now)

- YAML and TOML: they need parsers outside Go's standard library. Text mode
  covers them meanwhile.
- Editing or converting data: that's jq's job.
- `--json` in prism's envelope: peek reads data rather than code, so it has
  no refs to share yet.

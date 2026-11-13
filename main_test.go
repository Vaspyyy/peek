package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// peek runs the tool with args (and stdin, when not "") and returns its
// output and exit code.
func peek(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	code := run(args, strings.NewReader(stdin), stdin == "", w)
	w.Flush()
	return b.String(), code
}

func file(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func wants(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			t.Errorf("output lacks %q:\n%s", p, out)
		}
	}
}

func lacks(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(out, p) {
			t.Errorf("output has %q:\n%s", p, out)
		}
	}
}

const doc = `{
  "name": "aero-run",
  "levels": [
    {"id": "level_01", "beats": [{"t": 0.5, "kind": "tap"}, {"t": 2, "kind": "hold"}]},
    {"id": "level_02", "beats": [{"t": 1, "kind": "tap"}], "critic": {"score": 7}},
    {"id": "level_03", "beats": [{"t": 212, "kind": "tap"}, {"t": 3, "kind": "slide"}, {"t": 4, "kind": "tap"}]}
  ],
  "settings": {"volume": 0.8, "lang": null, "a key": true}
}`

func TestShape(t *testing.T) {
	p := file(t, "data.json", doc)
	out, code := peek(t, "", p)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out,
		"JSON, ", "object with 3 keys",
		`.name `, `"aero-run"`,
		".levels ", "array of 3 objects",
		".levels[].id", `"level_01", "level_02", "level_03"`,
		".levels[].beats ", "array of 1 to 3 objects",
		".levels[].beats[].t", "0.5 to 212",
		".levels[].beats[].kind", `"tap" ×4, "hold" ×1, "slide" ×1`,
		".levels[].critic ", "in 1 of 3",
		".levels[].critic.score", "7",
		`.settings["a key"]`, "true",
		".settings.lang", "null",
		"next: peek "+shellQuote(p)+" '.levels[0]'")
}

func TestPaths(t *testing.T) {
	p := file(t, "data.json", doc)
	for _, c := range []struct {
		path string
		want []string
	}{
		{".levels[1]", []string{"object with 3 keys", `"id": "level_02"`, `"critic": {`}},
		{".levels[-1].id", []string{`"level_03"`}},
		{".levels[].id", []string{"3 values", `"level_01"`, `"level_03"`}},
		{`.settings["a key"]`, []string{"a boolean", "true"}},
		{`.settings."a key"`, []string{"true"}},
		{".levels[].beats[].kind", []string{"6 values"}},
		{".levels[0].beats[].t", []string{"2 values", "0.5\n2\n"}},
		{".", []string{"object with 3 keys", `"name": "aero-run"`}},
	} {
		out, code := peek(t, "", p, c.path)
		if code != 0 {
			t.Errorf("%s: exit %d:\n%s", c.path, code, out)
			continue
		}
		wants(t, out, c.want...)
	}
	for _, c := range []struct{ path, want string }{
		{".levles", `no key "levles" in .; its keys: name, levels, settings`},
		{".levels[7]", ".levels has 3 elements"},
		{".name.x", ".name is a string, not an object"},
		{".name[]", ".name is a string, which [] can't go into"},
	} {
		out, code := peek(t, "", p, c.path)
		if code != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want %q:\n%s", c.path, code, c.want, out)
		}
	}
	if out, code := peek(t, "", p, ".levels[x]"); code != 2 || !strings.Contains(out, "isn't an index") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestPrettyClips(t *testing.T) {
	long := strings.Repeat("x", 500)
	var arr []string
	for i := 0; i < 30; i++ {
		arr = append(arr, fmt.Sprint(i))
	}
	p := file(t, "a.json", fmt.Sprintf(`{"s": %q, "a": [%s]}`, long, strings.Join(arr, ",")))
	out, _ := peek(t, "", p, ".")
	wants(t, out, "… +300 chars")
	out, _ = peek(t, "", p, ".a")
	wants(t, out, "array of 30 numbers", "… 20 more")
	out, _ = peek(t, "", p, "-b", "0", ".s")
	wants(t, out, strings.Repeat("x", 500)+`"`)
}

func TestBigValueGivesShape(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items": [`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id": %d, "tags": ["a", "b"], "meta": {"x": %d, "y": "z"}}`, i, i)
	}
	b.WriteString("]}")
	p := file(t, "big.json", b.String())
	out, code := peek(t, "", p, ".items[0]")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	wants(t, out, `"id": 0`)
	out, _ = peek(t, "", p, "-b", "8", ".items[0]")
	wants(t, out, "too long to print", ".items[0].meta.x")
}

func TestMap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"users": {`)
	for i := 0; i < 60; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"u%d": {"name": "n%d", "age": %d}`, i, i, 20+i%5)
	}
	b.WriteString("}}")
	p := file(t, "m.json", b.String())
	out, _ := peek(t, "", p)
	wants(t, out, `.users `, `60 keys like "u0", "u1", "u2"`, ".users[].name", ".users[].age", "20 to 24")
	out, _ = peek(t, "", p, ".users[].age")
	wants(t, out, "60 values: number")
}

func TestBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%d": {"a": {"b": %d}}`, i, i)
	}
	b.WriteString("}")
	p := file(t, "deep.json", b.String())
	out, _ := peek(t, "", p, "-b", "40")
	wants(t, out, ".k0 ", ".k29 ", ".k0.a ", "more paths (a path shows one part")
	lacks(t, out, ".k0.a.b")
	if n := strings.Count(out, "\n"); n > 41 {
		t.Errorf("%d lines for a budget of 40", n)
	}
	out, _ = peek(t, "", p, "-b", "0")
	wants(t, out, ".k29.a.b")
}

func TestInvalidJSON(t *testing.T) {
	p := file(t, "bad.json", "{\n  \"a\": 1,\n  \"b\": ]\n}\n")
	out, code := peek(t, "", p)
	if code != 2 || !strings.Contains(out, "not valid JSON: line 3, column") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestStdin(t *testing.T) {
	out, code := peek(t, `[{"a": 1}, {"a": 2, "b": "x"}]`, ".[].a")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "peek: stdin .[].a  2 values", "1\n2\n")
	out, _ = peek(t, `[{"a": 1}, {"a": 2, "b": "x"}]`)
	wants(t, out, "array of 2 objects", ".[].a", ".[].b", "in 1 of 2")
}

func TestJSONLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		kind := []string{"user", "assistant", "assistant"}[i%3]
		fmt.Fprintf(&b, `{"type": %q, "n": %d, "message": {"content": [{"type": "text"}, {"type": "tool_use", "name": "Bash"}]}}`+"\n", kind, i)
	}
	b.WriteString("not json\n\n")
	p := file(t, "t.jsonl", b.String())
	out, code := peek(t, "", p)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "JSON Lines, 50 records", "1 line not JSON (first: line 51)",
		".type", `"assistant" ×33, "user" ×17`,
		".message.content[].type", `"text" ×50, "tool_use" ×50`,
		".message.content[].name", "in 50 of 100")
	out, _ = peek(t, "", p, ".message.content[1].name")
	wants(t, out, ".message.content[1].name: 50 values from 50 records", `string  "Bash" ×50`)
	out, _ = peek(t, "", p, ".n")
	wants(t, out, "number  0 to 49")
	out, code = peek(t, "", p, ".nope")
	if code != 1 || !strings.Contains(out, `record 1: no key "nope"`) {
		t.Errorf("exit %d:\n%s", code, out)
	}
	// detected by content too
	p2 := file(t, "records", b.String())
	out, _ = peek(t, "", p2)
	wants(t, out, "JSON Lines, 50 records")
}

func TestCSV(t *testing.T) {
	p := file(t, "s.csv", "name,level,score,passed,note\nana,3,88.5,true,\nbo,12,40,false,\"late, fast\"\ncy,1,99.9,true,late\n")
	out, code := peek(t, "", p)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "CSV, 3 rows, 5 columns (with a header)",
		"level", "number   1 to 12", "passed", "true ×2, false ×1", "note", "empty ×1",
		"first rows", "name | level | score | passed | note", "bo   | 12")
	p = file(t, "t.tsv", "a\tb\n1\t2\n3\t4\n")
	out, _ = peek(t, "", p)
	wants(t, out, "TSV, 2 rows, 2 columns (with a header)")
	p = file(t, "semi", "x;y;z\n1;2;3\n4;5;6\n")
	out, _ = peek(t, "", p)
	wants(t, out, `CSV (separated by ';'), 2 rows, 3 columns`)
}

func TestBinary(t *testing.T) {
	p := file(t, "a.png", "\x89PNG\r\n\x1a\nxxxx")
	out, _ := peek(t, "", p)
	wants(t, out, "binary, 12 B, PNG image")
	p = file(t, "x.log", "a\x00b")
	out, _ = peek(t, "", p)
	wants(t, out, "binary")
}

func TestArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-b", "x", "a"}, "-b takes a number"},
		{[]string{"--as", "yaml", "a"}, "--as takes json"},
		{[]string{"-z"}, "unknown flag -z"},
		{[]string{"a", "b", "c"}, "one file at a time"},
		{[]string{"-g", "x", "a", "1-2"}, "can't go together"},
		{[]string{"-b"}, "-b needs a value"},
	} {
		out, code := peek(t, "", c.args...)
		if code != 2 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, want %q:\n%s", c.args, code, c.want, out)
		}
	}
	if out, code := peek(t, ""); code != 2 || !strings.Contains(out, "give a file") {
		t.Errorf("no args: exit %d:\n%s", code, out)
	}
	if out, code := peek(t, "", t.TempDir()); code != 2 || !strings.Contains(out, "is a folder") {
		t.Errorf("folder: exit %d:\n%s", code, out)
	}
}

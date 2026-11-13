package main

// A case for each defect the first reviews of peek 0.1.0 found (GPT-6.1
// Terra, Opus 5.5 and Sonnet 5.5, 2026-11-13).

import (
	"fmt"
	"strings"
	"testing"
)

func TestMixedMapAndObjects(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"x": 2`)
	for i := 0; i <= 40; i++ {
		fmt.Fprintf(&big, `, "k%d": %d`, i, i)
	}
	big.WriteString("}")
	p := file(t, "mixed.json", `{"items": [{"x": 1}, `+big.String()+`]}`)
	out, _ := peek(t, "", p)
	wants(t, out, ".items[].x", "1 to 2")
	lacks(t, out, "keys like")
}

func TestLongStringsCountedWhole(t *testing.T) {
	x := strings.Repeat("x", 400)
	p := file(t, "long.json", fmt.Sprintf(`{"a": [%q, %q]}`, x+"A", x+"B"))
	out, _ := peek(t, "", p)
	lacks(t, out, "×2")
	wants(t, out, ".a ")
}

func TestLowercaseLevels(t *testing.T) {
	for _, c := range []struct {
		line string
		want int
	}{
		{"warning: build failed", 1},
		{"warning[unused]: x failed", 1},
		{"error[E0425]: cannot find value", 2},
		{"note: the error is here", 0},
		{`{"level":"warn","msg":"request failed"}`, 1},
		{"time=1 level=info msg=\"0 errors\"", 0},
		{"level=error msg=disk", 2},
	} {
		if got := severity(c.line); got != c.want {
			t.Errorf("severity(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestNestedArrays(t *testing.T) {
	p := file(t, "nested.json", `{"x": [[1, 2], [3]]}`)
	out, _ := peek(t, "", p)
	wants(t, out, ".x ", "array of 2 arrays", ".x[]", "array of 1 to 2 numbers", "1 to 3")
	p = file(t, "nums.json", `[1, 2, 3]`)
	out, _ = peek(t, "", p)
	wants(t, out, "array of 3 numbers", "1 to 3")
}

func TestTinyBudgets(t *testing.T) {
	p := file(t, "log.txt", strings.Repeat("routine work\n", 80)+"ERROR: a\nERROR: b x\nERROR: c y z\nERROR: d\n")
	if out, code := peek(t, "", "-b", "1", "-g", "routine", p); code != 0 || !strings.Contains(out, "more (-b 0 shows all)") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	out, _ := peek(t, "", "-b", "2", p)
	wants(t, out, "left out for the budget: errors")

	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 15; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%d": %d`, i, i)
	}
	b.WriteString("}")
	p = file(t, "tiny.json", b.String())
	out, _ = peek(t, "", "-b", "1", p)
	lacks(t, out, ".k14")
	wants(t, out, "more paths")
}

func TestHeaderOnlyCSV(t *testing.T) {
	p := file(t, "header.csv", "a,b\n")
	out, _ := peek(t, "", "--as", "csv", p)
	wants(t, out, "0 rows, 2 columns (with a header)", "  a  empty", "  b  empty")
}

// The cases below are from the Sonnet 5.5 and Opus 5.5 reviews.

func TestNumbersAsWritten(t *testing.T) {
	p := file(t, "n.json", `{"t": [1700000000.123, 1700000099.456], "id": [1234567890123456789, 1234567890123456790]}`)
	out, _ := peek(t, "", p)
	wants(t, out, "1700000000.123 to 1700000099.456", "1234567890123456789 to 1234567890123456790")
	p = file(t, "z.csv", "zip,n\n01234,1\n02345,2\n10115,3\n")
	out, _ = peek(t, "", p)
	wants(t, out, `"01234"`)
}

func TestNoHTMLEscapes(t *testing.T) {
	p := file(t, "h.json", `{"a": "a&b<c>d"}`)
	out, _ := peek(t, "", p, ".a")
	wants(t, out, `"a&b<c>d"`)
}

func TestDetection(t *testing.T) {
	out, code := peek(t, "[10:00:00] [main/INFO]: hi\n[10:00:01] [main/WARN]: x\n")
	if code != 0 || !strings.Contains(out, "1 warning") {
		t.Errorf("a log starting with [ : exit %d:\n%s", code, out)
	}
	out, code = peek(t, "[ 50%] Building CXX object a.o\n[100%] Linking\n")
	if code != 0 || strings.Contains(out, "JSON") {
		t.Errorf("cmake output: exit %d:\n%s", code, out)
	}
	big := strings.Repeat("x", 50000)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, `{"n": %d, "s": %q}`+"\n", i, big)
	}
	out, code = peek(t, b.String())
	if code != 0 || !strings.Contains(out, "JSON Lines, 5 records") {
		t.Errorf("long JSON Lines: exit %d:\n%s", code, out)
	}
	out, _ = peek(t, "SELECT 1;\nSELECT 2;\nSELECT 3;\n")
	lacks(t, out, "CSV")
	out, code = peek(t, "\xef\xbb\xbf{\"a\": 1}")
	if code != 0 || !strings.Contains(out, ".a") {
		t.Errorf("BOM: exit %d:\n%s", code, out)
	}
	out, code = peek(t, `{"a": 1}`, ".")
	if code != 0 || !strings.Contains(out, `"a": 1`) {
		t.Errorf("| peek . : exit %d:\n%s", code, out)
	}
	out, code = peek(t, "{}", "./missing.json")
	if code != 2 || !strings.Contains(out, "no such file") {
		t.Errorf("./missing.json: exit %d:\n%s", code, out)
	}
}

func TestSeverityFormats(t *testing.T) {
	for _, c := range []struct {
		line string
		want int
	}{
		{"ValueError: invalid literal for int()", 2},
		{"Caused by: java.io.IOException: disk full", 2},
		{"E  AssertionError: assert 1 == 2", 2},
		{"DeprecationWarning: x is deprecated", 1},
		{"ok  \texample.com/x/errors\t0.01s", 0},
		{"=== RUN   TestDecode/error", 0},
		{"--- PASS: TestDecode/error (0.00s)", 0},
		{"test errors::parses_ok ... ok", 0},
		{"100% tests passed, 0 tests failed out of 4", 0},
		{"Tests run: 70, Failures: 3, Errors: 0, Skipped: 0", 2},
		{"0 failures, 1 error", 2},
		{"maven fail-fast mode", 0},
		{"./main.go:10:5: undefined: foo", 2},
		{"  2 failing", 2},
		{"12:00 INF request failed", 0},
		{"12:00 WRN disk almost full", 1},
		{`{"level":"info","msg":"fetch failed once"}`, 0},
	} {
		if got := severity(c.line); got != c.want {
			t.Errorf("severity(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestUUIDsMerge(t *testing.T) {
	var b strings.Builder
	ids := []string{"58cc1f3e-9b2a-4c3d-8e7f-0a1b2c3d4e5f", "b3fc0000-1111-4222-8333-444455556666", "9fceb0aa-a1b2-4c3d-9e8f-123456789abc"}
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "INFO session %s opened\n", ids[i%3])
	}
	out, _ := peek(t, b.String())
	wants(t, out, "×60", "session <n>-<n>-<n>-<n>-<n> opened")
}

func TestManyStringsTopIsRight(t *testing.T) {
	var vals []string
	for i := 0; i < 200; i++ {
		vals = append(vals, fmt.Sprintf("%q", fmt.Sprintf("u%d", i)))
	}
	for i := 0; i < 500; i++ {
		vals = append(vals, `"hot"`)
	}
	p := file(t, "s.json", "["+strings.Join(vals, ",")+"]")
	out, _ := peek(t, "", p)
	wants(t, out, `most often about "hot"`)
}

func TestWideRecordIsNotAMap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"id": 99, "name": "x", "on": true`)
	for i := 0; i < 42; i++ {
		fmt.Fprintf(&b, `, "f%d": %d`, i, i)
	}
	b.WriteString("}")
	p := file(t, "w.json", b.String())
	out, _ := peek(t, "", p)
	wants(t, out, ".id ", ".name ", ".on ")
	lacks(t, out, "keys like")
}

func TestMissedAlongThePath(t *testing.T) {
	p := file(t, "c.json", `{"levels": [{"critic": {"score": 1}}, {}, {}]}`)
	out, _ := peek(t, "", p, ".levels[].critic")
	wants(t, out, "2 values along the way had nothing there")
}

func TestBudgetFillsPartialLevels(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 3; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"g%d": {`, i)
		for j := 0; j < 20; j++ {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `"k%d": %d`, j, j)
		}
		b.WriteString("}")
	}
	b.WriteString("}")
	p := file(t, "p.json", b.String())
	out, _ := peek(t, "", p)
	if n := strings.Count(out, "\n"); n < 50 {
		t.Errorf("only %d lines used of about 60:\n%s", n, out)
	}
	lacks(t, out, "deeper")
}

func TestDeepNestingIsCheap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString(`{"a":`)
	}
	b.WriteString("1")
	for i := 0; i < 3000; i++ {
		b.WriteString("}")
	}
	p := file(t, "deep.json", b.String())
	out, code := peek(t, "", p, "-b", "0")
	if code != 0 || !strings.Contains(out, "nested deeper than 64 levels") || strings.Count(out, "\n") > 70 {
		t.Errorf("exit %d, %d lines:\n%.500s", code, strings.Count(out, "\n"), out)
	}
}

func TestStdinSizeAndHints(t *testing.T) {
	var b strings.Builder
	b.WriteString("a,b\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "%d,%d\n", i, i*2)
	}
	out, _ := peek(t, b.String())
	lacks(t, out, "64 KB")
	out, _ = peek(t, `{"x": [{"a": 1}]}`)
	lacks(t, out, "next:")
}

func TestGrepOnJSONRefused(t *testing.T) {
	p := file(t, "c.json", `{"a": 1}`)
	if out, code := peek(t, "", p, "-g", "a"); code != 2 || !strings.Contains(out, "-g and -t are for text") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestDuplicateKeyLastWins(t *testing.T) {
	p := file(t, "d.json", `{"dup": 1, "dup": 2}`)
	out, _ := peek(t, "", p, ".dup")
	wants(t, out, "\n2\n")
}

// The cases below are from GPT-6.1 Terra's re-check of cd284e6.

func TestShortCSVDelimiter(t *testing.T) {
	p := file(t, "short.csv", "name;age\nAlice;20\n")
	out, _ := peek(t, "", p)
	wants(t, out, "1 row, 2 columns (with a header)", "  age ")
}

func TestBrokenJSONOnStdin(t *testing.T) {
	out, code := peek(t, `{"x": 1,}`+"\n")
	if code != 2 || !strings.Contains(out, "not valid JSON") || !strings.Contains(out, "--as text") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestJSONLinesMissedAlong(t *testing.T) {
	out, _ := peek(t, `{"items":[{"x":1},{}]}`+"\n"+`{"items":[{"x":2},{"x":3}]}`+"\n", ".items[].x")
	wants(t, out, "3 values from 2 records; in the others, 1 value along the way had nothing there")
}

func TestDuplicateKeyCard(t *testing.T) {
	out, _ := peek(t, `{"x":1,"x":2}`, "--as", "json")
	wants(t, out, ".x  number  2")
	lacks(t, out, "1 to 2")
	out, _ = peek(t, `{"x":1,"x":2}`, "--as", "json", ".")
	wants(t, out, `"x": 2`)
	lacks(t, out, `"x": 1`)
}

func TestLocatedSeverity(t *testing.T) {
	for _, c := range []struct {
		line string
		want int
	}{
		{"src/main.rs:4:2: warning: unused variable", 1},
		{"src/main.rs:5:2: note: details", 0},
		{"src/main.rs:6:2: error: bad", 2},
		{"x.c:3:1: error[E1]: bad", 2},
		{"./main.go:10:5: undefined: foo", 2},
		{"ok  pkg/ERROR 0.1s", 0},
	} {
		if got := severity(c.line); got != c.want {
			t.Errorf("severity(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestEmptyJSONLines(t *testing.T) {
	p := file(t, "e.jsonl", "")
	out, code := peek(t, "", p)
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

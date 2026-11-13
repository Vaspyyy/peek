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

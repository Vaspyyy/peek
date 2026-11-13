package main

import (
	"fmt"
	"strings"
	"testing"
)

func logText() string {
	var b strings.Builder
	b.WriteString("\nstarting run\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "tick %d pos=(%d.5, -%d.25)\n", i, i%7, i%3)
		if i%1000 == 999 {
			fmt.Fprintf(&b, "ERROR: lost sync at tick %d\n", i)
		}
		if i == 1500 {
			b.WriteString("WARNING: slow frame 42 ms\n")
		}
	}
	b.WriteString("[main/WARN] Failed to set window position\n")
	b.WriteString("[worker/INFO] loaded Exception handler\n")
	b.WriteString("| level 2 | FAIL | missed\n")
	b.WriteString("RUN 1 done\nRUN 2 done\n0 errors in replay\nno warnings\nall done\n")
	return b.String()
}

func TestLogCard(t *testing.T) {
	p := file(t, "run.log", logText())
	out, code := peek(t, "", p)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out,
		"3,014 lines", "4 errors, 2 warnings",
		"errors (4, 2 kinds)", "ERROR: lost sync at tick 999   ×3, also L", "| level 2 | FAIL | missed",
		"warnings", "WARNING: slow frame 42 ms", "[main/WARN] Failed to set window position",
		"most repeated", "×3,000", "tick <n> pos=(<n>, <n>)",
		"first lines", "starting run",
		"last lines", "all done",
		"next: peek ")
	lacks(t, out, "Exception handler   ×", "0 errors in replay   ×")
	// the INFO line with "Exception" and "0 errors" aren't errors
	if strings.Contains(out[:strings.Index(out, "warnings")], "Exception handler") {
		t.Errorf("an INFO line counted as an error:\n%s", out)
	}
}

func TestShortTextWhole(t *testing.T) {
	p := file(t, "notes.txt", "one\ntwo\nthree\n")
	out, _ := peek(t, "", p)
	wants(t, out, "3 lines", "  1  one\n  2  two\n  3  three\n")
}

func TestGrep(t *testing.T) {
	p := file(t, "run.log", logText())
	out, code := peek(t, "", p, "-g", `RUN \d`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "2 matches of 'RUN \\d' in 1 kind", "RUN 1 done   ×2, also L")
	out, _ = peek(t, "", p, "-g", "run", "-i")
	wants(t, out, "starting run", "RUN 1 done")
	if out, code := peek(t, "", p, "-g", "zzz"); code != 1 || !strings.Contains(out, "0 matches") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if out, code := peek(t, "", p, "-g", "("); code != 2 || !strings.Contains(out, "bad pattern") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestLineRangeAndTail(t *testing.T) {
	p := file(t, "run.log", logText())
	out, code := peek(t, "", p, "3-4")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wants(t, out, "lines 3-4", "  3  tick 0 pos=(0.5, -0.25)\n  4  tick 1")
	out, _ = peek(t, "", p, "-t", "2")
	wants(t, out, "the last 2 lines", "no warnings\n", "all done\n")
	if out, code := peek(t, "", p, "9000-9001"); code != 1 || !strings.Contains(out, "no such lines") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if out, code := peek(t, "", p, "x-y"); code != 2 {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestClipLine(t *testing.T) {
	long := "start " + strings.Repeat("x", 1000) + " the reason"
	got := clipLine(long)
	if !strings.HasPrefix(got, "start ") || !strings.HasSuffix(got, " the reason") || !strings.Contains(got, " chars)… ") {
		t.Errorf("clipLine = %q", got)
	}
	if len([]rune(got)) > lineClip+30 {
		t.Errorf("clipped line is %d characters", len([]rune(got)))
	}
}

func TestTemplate(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"tick 12 pos=(3.5, -0.25)", "tick <n> pos=(<n>, <n>)"},
		{"load level_02 done", "load level_<n> done"},
		{"commit 3f9a2c1d ok", "commit <n> ok"},
		{"addr 0x1f3a", "addr <n>"},
		{"a    b\t\tc", "a b c"},
		{"x-1 y", "x-<n> y"},
		{"no numbers here", "no numbers here"},
	} {
		if got := template(c.in); got != c.want {
			t.Errorf("template(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSeverity(t *testing.T) {
	for _, c := range []struct {
		line string
		want int
	}{
		{"ERROR: x", 2},
		{"error: cannot find module", 2},
		{"--- FAIL: TestX (0.01s)", 2},
		{"not ok 3 - parses", 2},
		{"Traceback (most recent call last):", 2},
		{"[main/WARN] Failed to load", 1},
		{"[worker/INFO] caught Exception", 0},
		{"level=DEBUG msg=error retry", 0},
		{"warning: unused variable", 1},
		{"0 errors, 0 warnings", 0},
		{"no errors found", 0},
		{"errors: 0", 0},
		{"all good", 0},
		{"error_count=3", 0},
	} {
		if got := severity(c.line); got != c.want {
			t.Errorf("severity(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

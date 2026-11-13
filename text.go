package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Limits for text.
const (
	lineClip     = 160    // characters of a line shown
	maxTemplates = 100000 // distinct templates tracked
	alsoLines    = 3      // more line numbers shown for a group
)

var (
	// a level a logger wrote, which wins over the words in the message
	reLevel    = regexp.MustCompile(`\b(TRACE|DEBUG|FINE|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|CRITICAL|CRIT|SEVERE|PANIC)\b`)
	reError    = regexp.MustCompile(`(?i)\b(errors?|fatal|panic(ked)?|exceptions?|traceback|fail(s|ed|ures?)?|segfault)\b|^not ok\b`)
	reNotError = regexp.MustCompile(`(?i)\b(0|no|zero) (errors?|failures?|failed)\b|\b(errors?|failures?|failed)[:=] ?0\b`)
	reWarn     = regexp.MustCompile(`(?i)\bwarn(ing|ings|s)?\b`)
	reNotWarn  = regexp.MustCompile(`(?i)\b(0|no|zero) warn(ing|ings|s)?\b|\bwarn(ing|ings|s)?[:=] ?0\b`)
)

// group is the lines that share a template.
type group struct {
	first, last int // line numbers
	count       int
	text        string // the first line
	more        []int  // further line numbers, up to alsoLines
}

func (g *group) add(n int, text string) {
	if g.count == 0 {
		g.first, g.text = n, text
	} else if len(g.more) < alsoLines {
		g.more = append(g.more, n)
	}
	g.last = n
	g.count++
}

// groups keeps groups by template, in the order they first appeared.
type groups struct {
	by    map[string]*group
	order []string
	lines int // lines added
	over  bool
}

func (gs *groups) add(tmpl string, n int, text string) {
	gs.lines++
	if gs.by == nil {
		gs.by = map[string]*group{}
	}
	g := gs.by[tmpl]
	if g == nil {
		if len(gs.order) >= maxTemplates {
			gs.over = true
			return
		}
		g = &group{}
		gs.by[tmpl] = g
		gs.order = append(gs.order, tmpl)
	}
	g.add(n, text)
}

// numbered is a line with its number.
type numbered struct {
	n    int
	text string
}

// textScan is one pass over a text.
type textScan struct {
	lines, longest int
	bytes          int64
	all            groups
	errs, warns    groups
	head           []numbered
	tail           []numbered // ring of the last tailN lines
	tailN          int
	match          *regexp.Regexp
	hits           groups
	from, to       int // a line range to keep
	kept           []numbered
	keepAll        int // keep every line while there are no more than this
}

func (t *textScan) run(r io.Reader) error {
	br := bufio.NewReaderSize(r, 1<<20)
	n := 0
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			n++
			t.bytes += int64(len(line))
			line = strings.TrimRight(line, "\r\n")
			t.line(n, line)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	t.lines = n
	return nil
}

func (t *textScan) line(n int, line string) {
	t.longest = max(t.longest, len(line))
	if t.from > 0 {
		if n >= t.from && n <= t.to {
			t.kept = append(t.kept, numbered{n, line})
		}
		return
	}
	if t.keepAll > 0 && n <= t.keepAll+1 {
		t.kept = append(t.kept, numbered{n, line})
	}
	if t.tailN > 0 {
		if len(t.tail) < t.tailN {
			t.tail = append(t.tail, numbered{n, line})
		} else {
			t.tail[(n-1)%t.tailN] = numbered{n, line}
		}
	}
	if t.match != nil {
		if t.match.MatchString(line) {
			t.hits.add(template(line), n, line)
		}
		return
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(t.head) < 3 {
		t.head = append(t.head, numbered{n, line})
	}
	tmpl := template(line)
	t.all.add(tmpl, n, line)
	switch severity(line) {
	case 2:
		t.errs.add(tmpl, n, line)
	case 1:
		t.warns.add(tmpl, n, line)
	}
}

// severity is 2 for an error line, 1 for a warning, 0 otherwise. A level the
// logger wrote ("[main/WARN]", "ERROR:", "level=INFO") decides; without
// one, the words do ("failed", "exception", "warning"), but not in "0 errors"
// or "no warnings".
func severity(line string) int {
	if m := reLevel.FindString(line); m != "" {
		switch m {
		case "WARN", "WARNING":
			return 1
		case "ERROR", "ERR", "FATAL", "CRITICAL", "CRIT", "SEVERE", "PANIC":
			return 2
		}
		return 0
	}
	if reError.MatchString(line) && !reNotError.MatchString(line) {
		return 2
	}
	if reWarn.MatchString(line) && !reNotWarn.MatchString(line) {
		return 1
	}
	return 0
}

// lastLines returns the ring of last lines in order.
func (t *textScan) lastLines() []numbered {
	out := append([]numbered(nil), t.tail...)
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	return out
}

// template replaces the parts of a line that vary between otherwise equal
// lines (numbers with their signs, hex values and IDs) with <n>, and runs of
// spaces with one.
func template(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	space := false
	for i < len(s) {
		c := s[i]
		switch {
		case isWord(c):
			j := i
			for j < len(s) && isWord(s[j]) {
				j++
			}
			b.WriteString(normWord(s[i:j]))
			i = j
		case c == ' ' || c == '\t':
			if !space {
				b.WriteByte(' ')
			}
			i++
		case (c == '-' || c == '+') && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && (i == 0 || !isWord(s[i-1])):
			i++ // a number's sign: -3 and 3 are the same kind of line
		default:
			b.WriteByte(c)
			i++
		}
		space = c == ' ' || c == '\t'
	}
	return b.String()
}

func isWord(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '.'
}

// normWord turns a hex-looking word into <n> and every run of digits in
// other words into <n>: "0x1f3a", "3f9a2c1d" and "12.5" become "<n>",
// "level_02" becomes "level_<n>".
func normWord(w string) string {
	hasDigit := strings.IndexAny(w, "0123456789") >= 0
	if !hasDigit {
		return w
	}
	if hexLike(w) {
		return "<n>"
	}
	var b strings.Builder
	i := 0
	for i < len(w) {
		if w[i] >= '0' && w[i] <= '9' {
			j := i
			for j < len(w) && (w[j] >= '0' && w[j] <= '9' || w[j] == '.' && j+1 < len(w) && w[j+1] >= '0' && w[j+1] <= '9') {
				j++
			}
			b.WriteString("<n>")
			i = j
			continue
		}
		b.WriteByte(w[i])
		i++
	}
	return b.String()
}

func hexLike(w string) bool {
	if strings.HasPrefix(w, "0x") || strings.HasPrefix(w, "0X") {
		return len(w) > 2
	}
	if len(w) < 7 {
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// clipLine shortens a long line for display: its start and its end, and how
// much was left out between them.
func clipLine(s string) string {
	r := []rune(s)
	if len(r) <= lineClip {
		return s
	}
	// the start says where, the end often says why: keep both
	head, tail := lineClip*2/3, lineClip/3
	return string(r[:head]) + fmt.Sprintf(" …(%s chars)… ", count(len(r)-head-tail)) + string(r[len(r)-tail:])
}

// groupLine is "L1203  text   ×2, also L1450".
func groupLine(g *group, w int) string {
	line := fmt.Sprintf("  %-*s  %s", w, fmt.Sprintf("L%d", g.first), clipLine(g.text))
	if g.count == 1 {
		return line
	}
	line += "   ×" + count(g.count)
	var ls []string
	for _, n := range g.more {
		ls = append(ls, fmt.Sprintf("L%d", n))
	}
	if g.count-1 > len(g.more) && g.last != g.more[len(g.more)-1] {
		ls = append(ls, fmt.Sprintf("… L%d", g.last))
	}
	return line + ", also " + strings.Join(ls, ", ")
}

// renderText writes the card of a text scan within budget lines (0: all).
func renderText(w io.Writer, name string, t *textScan, budget int, next *[]string) {
	head := fmt.Sprintf("peek: %s  %s, %s", name, plural(t.lines, "line"), sizeText(t.bytes))
	if t.longest > lineClip {
		head += fmt.Sprintf(", longest %s chars", count(t.longest))
	}
	if t.errs.lines > 0 || t.warns.lines > 0 {
		head += fmt.Sprintf("; %s, %s", plural(t.errs.lines, "error"), plural(t.warns.lines, "warning"))
	}
	fmt.Fprintln(w, head)

	// a short text is shown whole
	if t.keepAll > 0 && t.lines <= t.keepAll {
		width := len(fmt.Sprint(t.lines))
		for _, l := range t.kept {
			fmt.Fprintf(w, "  %*d  %s\n", width, l.n, clipLine(l.text))
		}
		return
	}

	room := budget
	if room <= 0 {
		room = 1 << 30
	}
	room-- // the header
	nw := len(fmt.Sprintf("L%d", t.lines))
	section := func(title string, gs *groups, limit int, sortBy func([]string)) {
		if len(gs.order) == 0 || room < 2 {
			return
		}
		order := append([]string(nil), gs.order...)
		if sortBy != nil {
			sortBy(order)
		}
		show := min(len(order), limit, room-1)
		if len(order) > show && show > 1 && room-1 == show {
			show-- // room for the "more" line
		}
		fmt.Fprintln(w, title)
		room--
		for _, tm := range order[:show] {
			fmt.Fprintln(w, groupLine(gs.by[tm], nw))
			room--
		}
		if rest := len(order) - show; rest > 0 && room > 0 {
			fmt.Fprintf(w, "  … %s more\n", plural(rest, "kind"))
			room--
		}
	}
	errTitle := "errors"
	if len(t.errs.order) < t.errs.lines {
		errTitle = fmt.Sprintf("errors (%s, %s)", count(t.errs.lines), plural(len(t.errs.order), "kind"))
	}
	section(errTitle, &t.errs, 12, nil)
	warnTitle := "warnings"
	if len(t.warns.order) < t.warns.lines {
		warnTitle = fmt.Sprintf("warnings (%s, %s)", count(t.warns.lines), plural(len(t.warns.order), "kind"))
	}
	section(warnTitle, &t.warns, 6, nil)

	// the most repeated templates that aren't errors or warnings
	rep := groups{by: map[string]*group{}}
	for _, tm := range t.all.order {
		g := t.all.by[tm]
		if g.count < 3 || t.errs.by[tm] != nil || t.warns.by[tm] != nil {
			continue
		}
		rep.by[tm] = g
		rep.order = append(rep.order, tm)
	}
	sort.SliceStable(rep.order, func(i, j int) bool { return rep.by[rep.order[i]].count > rep.by[rep.order[j]].count })
	if len(rep.order) > 0 && room > 4 {
		show := min(len(rep.order), 5, room-4)
		fmt.Fprintln(w, "most repeated")
		room--
		for _, tm := range rep.order[:show] {
			g := rep.by[tm]
			fmt.Fprintf(w, "  ×%-6s %-*s  %s\n", count(g.count), nw*2+1, fmt.Sprintf("L%d-%d", g.first, g.last), clipLine(tm))
			room--
		}
	}

	// the first and last lines
	if len(t.head) > 0 && room > 4 {
		fmt.Fprintln(w, "first lines")
		room--
		for _, l := range t.head[:min(len(t.head), 3)] {
			fmt.Fprintf(w, "  %*d  %s\n", nw-1, l.n, clipLine(l.text))
			room--
		}
	}
	last := t.lastLines()
	if len(last) > 0 && room > 1 {
		skip := 0
		if len(t.head) > 0 && len(last) > 0 {
			for skip < len(last) && last[skip].n <= t.head[len(t.head)-1].n {
				skip++
			}
		}
		last = last[skip:]
		if len(last) > room-1 {
			last = last[len(last)-(room-1):]
		}
		if len(last) > 0 {
			fmt.Fprintln(w, "last lines")
			for _, l := range last {
				fmt.Fprintf(w, "  %*d  %s\n", nw-1, l.n, clipLine(l.text))
			}
		}
	}
	if len(t.errs.order) > 0 {
		*next = append(*next, fmt.Sprintf("next: peek %s %d-%d   the lines around the first error", shellQuote(name), max(1, t.errs.by[t.errs.order[0]].first-5), t.errs.by[t.errs.order[0]].first+15))
	}
}

// renderHits writes the lines a pattern matched, grouped by template.
func renderHits(w io.Writer, name string, t *textScan, pattern string, budget int) int {
	gs := &t.hits
	fmt.Fprintf(w, "peek: %s  %s; %s of %s", name, plural(t.lines, "line"), plural(gs.lines, "match"), shellQuote(pattern))
	if len(gs.order) < gs.lines {
		fmt.Fprintf(w, " in %s", plural(len(gs.order), "kind"))
	}
	fmt.Fprintln(w)
	if gs.lines == 0 {
		return 1
	}
	nw := len(fmt.Sprintf("L%d", t.lines))
	show := len(gs.order)
	if budget > 0 && show > budget-1 {
		show = budget - 2
	}
	for _, tm := range gs.order[:show] {
		fmt.Fprintln(w, groupLine(gs.by[tm], nw))
	}
	if rest := len(gs.order) - show; rest > 0 {
		fmt.Fprintf(w, "  … %s more (-b 0 shows all)\n", plural(rest, "kind"))
	}
	return 0
}

// renderLines writes kept lines, clipped.
func renderLines(w io.Writer, name string, t *textScan, what string) int {
	fmt.Fprintf(w, "peek: %s  %s, %s\n", name, plural(t.lines, "line"), what)
	if len(t.kept) == 0 {
		fmt.Fprintf(w, "  no such lines: it has %s\n", plural(t.lines, "line"))
		return 1
	}
	width := len(fmt.Sprint(t.kept[len(t.kept)-1].n))
	for _, l := range t.kept {
		fmt.Fprintf(w, "  %*d  %s\n", width, l.n, clipLine(l.text))
	}
	return 0
}

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
	// a level a logger or compiler wrote, which wins over the words in the
	// message: "warning: x failed" (gcc, cargo), "level=info", "[main/WARN]"
	reLead  = regexp.MustCompile(`(?i)^\s*(error|err|fatal|panic|warning|warn|info|note|notice|debug|trace|hint|help)(\[[^\]]*\])?:`)
	reKV    = regexp.MustCompile(`(?i)\b(?:level|lvl|severity)"?\s*[=:]\s*"?([a-z]+)`)
	reLevel = regexp.MustCompile(`\b(TRACE|TRC|DEBUG|DBG|FINE|INFO|INF|NOTICE|WARN|WARNING|WRN|ERROR|ERR|FATAL|FTL|CRITICAL|CRIT|SEVERE|PANIC)\b`)
	// lines of passing tests, whatever their names say ("--- PASS: TestError")
	rePass = regexp.MustCompile(`^\s*(ok\s|--- PASS|=== (RUN|PAUSE|CONT|NAME)\b|PASS\s*$|test .* \.\.\. ok\s*$)`)
	// a compiler's or linter's "path:line:col: message"
	reLocated = regexp.MustCompile(`^\S+\.\w+:\d+(:\d+)?: (?:([A-Za-z]+)(?:\[[^\]]*\])?:)?`)
	// CamelCase names of errors and warnings: ValueError, IOException
	reErrName  = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*(Error|Exception)\b`)
	reWarnName = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*Warning\b`)
	// words that aren't failures: "fail-fast", "failover"
	reNotFail  = regexp.MustCompile(`(?i)\bfail-(fast|safe|over)\b|\bfailover\b`)
	reFailing  = regexp.MustCompile(`\b[1-9]\d* failing\b`)
	reCaused   = regexp.MustCompile(`^\s*Caused by: `)
	reError    = regexp.MustCompile(`(?i)\b(errors?|fatal|panic(ked)?|exceptions?|traceback|fail(s|ed|ures?)?|segfault)\b|^not ok\b`)
	reNotError = regexp.MustCompile(`(?i)\b(0|no|zero)( \w+)? (errors?|failures?|failed)\b|\b(errors?|failures?|failed)[:=] ?0\b`)
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
	if rePass.MatchString(line) {
		return 0 // a passing test, whatever its name says ("ok  pkg/ERROR")
	}
	if m := reLead.FindStringSubmatch(line); m != nil {
		return levelOf(m[1])
	}
	if m := reKV.FindStringSubmatch(line); m != nil && knownLevel(m[1]) {
		return levelOf(m[1])
	}
	if m := reLevel.FindString(line); m != "" {
		return levelOf(m)
	}
	if m := reLocated.FindStringSubmatch(line); m != nil {
		// "path:line[:col]: message": a level word there says what it is
		// ("warning:", "note:", "DeprecationWarning:"); without one,
		// "path:line:col:" is how Go's compiler writes an error, and
		// otherwise the message's own words decide, below
		lv := strings.ToLower(m[2])
		switch {
		case lv == "warning" || lv == "warn" || strings.HasSuffix(lv, "warning"):
			return 1
		case lv == "note" || lv == "info" || lv == "help" || lv == "hint":
			return 0
		case lv == "error" || lv == "fatal" || strings.HasSuffix(lv, "error") || strings.HasSuffix(lv, "exception"):
			return 2
		case m[1] != "":
			return 2
		}
	}
	if reCaused.MatchString(line) {
		return 2
	}
	// "0 errors" and "Errors: 0" go, and so does the rest's meaning
	clean := reNotFail.ReplaceAllString(reNotError.ReplaceAllString(line, " "), " ")
	if errorWord(clean) || errorName(clean) || reFailing.MatchString(clean) {
		return 2
	}
	clean = reNotWarn.ReplaceAllString(line, " ")
	if reWarn.MatchString(clean) || reWarnName.MatchString(clean) {
		return 1
	}
	return 0
}

// errorWord finds an error word that isn't part of a name: "error" in
// "build error" counts, in "pkg/errors", "TestDecode/error" or
// "errors::parses_ok" it doesn't.
func errorWord(line string) bool {
	for _, m := range reError.FindAllStringIndex(line, -1) {
		if !inName(line, m[0], m[1]) {
			return true
		}
	}
	return false
}

// errorName finds a name like ValueError or IOException that is an error
// being reported: with a message after it ("IOException: disk full"), or
// on its own, but not as part of a file or class path
// ("ValidationError.swift", "at com.x.AppException.<init>").
func errorName(line string) bool {
	for _, m := range reErrName.FindAllStringIndex(line, -1) {
		if m[1] < len(line) && line[m[1]] == ':' && (m[1]+1 == len(line) || line[m[1]+1] != ':') {
			return true
		}
		if !inName(line, m[0], m[1]) {
			return true
		}
	}
	return false
}

// inName tells whether line[a:b] is part of a name: a path ("pkg/errors"),
// a file or member ("errors.go", "x.errors"), a Rust path ("errors::x"), an
// identifier ("error_count"). A word that ends a sentence ("failed.") or a
// status ("1/FAILURE") isn't.
func inName(line string, a, b int) bool {
	alnum := func(i int) bool {
		if i < 0 || i >= len(line) {
			return false
		}
		c := line[i]
		return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	if a > 0 {
		switch line[a-1] {
		case '_':
			return true
		case '/':
			if !(a > 1 && line[a-2] >= '0' && line[a-2] <= '9') {
				return true
			}
		case '.':
			if alnum(a - 2) {
				return true
			}
		case ':':
			if a > 1 && line[a-2] == ':' {
				return true
			}
		}
	}
	if b < len(line) {
		switch line[b] {
		case '_', '/':
			return true
		case '.', '-':
			return alnum(b + 1)
		case ':':
			return b+1 < len(line) && line[b+1] == ':'
		}
	}
	return false
}

// knownLevel: a word a logger writes as a level, so "level=max" isn't one.
func knownLevel(w string) bool {
	switch strings.ToLower(w) {
	case "trace", "trc", "debug", "dbg", "fine", "info", "inf", "notice", "warn", "warning", "wrn",
		"error", "err", "fatal", "ftl", "critical", "crit", "severe", "panic", "emerg", "alert":
		return true
	}
	return false
}

// levelOf is the severity of a level's name.
func levelOf(level string) int {
	switch strings.ToLower(level) {
	case "warn", "warning", "wrn":
		return 1
	case "error", "err", "fatal", "ftl", "critical", "crit", "severe", "panic", "emerg", "alert":
		return 2
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
	if len(w) < 4 { // a UUID's shortest part has 4
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true // normWord only asks when the word has a digit
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
	if t.all.over {
		fmt.Fprintf(w, "… over %s kinds of lines: kinds that first appear after that aren't counted\n", count(maxTemplates))
	}

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
	var left []string // sections the budget left out
	section := func(title string, gs *groups, limit int, sortBy func([]string)) bool {
		if len(gs.order) == 0 || room < 2 {
			return false
		}
		order := append([]string(nil), gs.order...)
		if sortBy != nil {
			sortBy(order)
		}
		show := min(len(order), limit, room-1)
		if len(order) > show && show > 1 && room-1 == show {
			show-- // room for the "more" line
		}
		// in the order they first appeared (the first is often the cause),
		// but the two most frequent kinds are always among them
		pick := order[:show]
		if len(order) > show && show >= 3 {
			byCount := append([]string(nil), order...)
			sort.SliceStable(byCount, func(i, j int) bool { return gs.by[byCount[i]].count > gs.by[byCount[j]].count })
			keep := map[string]bool{}
			for _, tm := range order[:show-2] {
				keep[tm] = true
			}
			for _, tm := range byCount {
				if len(keep) == show {
					break
				}
				keep[tm] = true
			}
			pick = pick[:0:0]
			for _, tm := range order {
				if keep[tm] {
					pick = append(pick, tm)
				}
			}
		}
		fmt.Fprintln(w, title)
		room--
		for _, tm := range pick {
			fmt.Fprintln(w, groupLine(gs.by[tm], nw))
			room--
		}
		if rest := len(order) - show; rest > 0 && room > 0 {
			fmt.Fprintf(w, "  … %s more\n", plural(rest, "kind"))
			room--
		}
		return true
	}
	errTitle := "errors"
	if len(t.errs.order) < t.errs.lines {
		errTitle = fmt.Sprintf("errors (%s, %s)", count(t.errs.lines), plural(len(t.errs.order), "kind"))
	}
	if !section(errTitle, &t.errs, 12, nil) && len(t.errs.order) > 0 {
		left = append(left, "errors")
	}
	warnTitle := "warnings"
	if len(t.warns.order) < t.warns.lines {
		warnTitle = fmt.Sprintf("warnings (%s, %s)", count(t.warns.lines), plural(len(t.warns.order), "kind"))
	}
	if !section(warnTitle, &t.warns, 6, nil) && len(t.warns.order) > 0 {
		left = append(left, "warnings")
	}

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
		if rest := len(rep.order) - show; rest > 0 && room > 3 {
			fmt.Fprintf(w, "  … %s more repeated\n", plural(rest, "kind"))
			room--
		}
	} else if len(rep.order) > 0 {
		left = append(left, "most repeated lines")
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
	} else if len(last) > 0 {
		left = append(left, "last lines")
	}
	if len(left) > 0 {
		fmt.Fprintf(w, "… left out for the budget: %s (a larger -b shows them)\n", strings.Join(left, ", "))
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
		show = max(budget-2, 0)
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

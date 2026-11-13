// peek prints a short card about a data file or a log: the shape of a JSON
// document, the fields of JSON Lines records, the columns of a CSV file, or
// the errors, repeated lines and last lines of a log. A path drills into
// JSON; a pattern or a line range narrows a log.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"
)

const version = "0.1.0"

const defaultBudget = 60

type options struct {
	file, arg string // the file ("-" for standard input) and a path or line range
	budget    int
	pattern   string
	ignore    bool
	tail      int
	kind      string
	plain     bool
}

func main() {
	out := bufio.NewWriterSize(os.Stdout, 64<<10)
	code := run(os.Args[1:], os.Stdin, term.IsTerminal(int(os.Stdin.Fd())), out)
	out.Flush()
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdinTTY bool, out *bufio.Writer) int {
	o, code, msg := parse(args, stdinTTY)
	if msg != "" {
		if code == 0 {
			fmt.Fprint(out, msg)
		} else {
			fmt.Fprintln(out, "peek: "+msg)
		}
		return code
	}
	name := o.file
	var r *bufio.Reader
	var size int64 = -1
	if o.file == "-" {
		name = "stdin"
		r = bufio.NewReaderSize(stdin, 1<<20)
	} else {
		fi, err := os.Stat(o.file)
		if err != nil {
			fmt.Fprintf(out, "peek: %v\n", err)
			return 2
		}
		if fi.IsDir() {
			fmt.Fprintf(out, "peek: %s is a folder (glint -A %s maps it)\n", o.file, shellQuote(o.file))
			return 2
		}
		size = fi.Size()
		f, err := os.Open(o.file)
		if err != nil {
			fmt.Fprintf(out, "peek: %v\n", err)
			return 2
		}
		defer f.Close()
		r = bufio.NewReaderSize(f, 1<<20)
	}
	sample, _ := r.Peek(64 << 10)
	kind := o.kind
	if kind == "" {
		kind = detect(o.file, sample)
	}
	var next []string
	code = 0
	switch kind {
	case "binary":
		fmt.Fprintf(out, "peek: %s  binary, %s%s\n", name, sizeOr(size), magic(sample))
	case "json":
		code = runJSON(out, name, r, o, size, &next)
	case "jsonl":
		code = runJSONL(out, name, r, o, size, &next)
	case "csv", "tsv":
		delim := sniffDelim(sample)
		if kind == "tsv" {
			delim = '\t'
		} else if delim == 0 {
			delim = ','
		}
		if err := renderTable(out, name, r, delim, sizeFrom(size, sample), o.budget); err != nil {
			fmt.Fprintf(out, "peek: %s: %v\n", name, err)
			return 2
		}
	default:
		code = runText(out, name, r, o, &next)
	}
	for i, n := range next {
		if i == 2 {
			break
		}
		fmt.Fprintln(out, n)
	}
	return code
}

func sizeOr(n int64) string {
	if n < 0 {
		return "from stdin"
	}
	return sizeText(n)
}

func sizeFrom(n int64, sample []byte) int64 {
	if n < 0 {
		return int64(len(sample))
	}
	return n
}

// detect names the kind of a file by its extension, else by its content.
func detect(file string, sample []byte) string {
	if magic(sample) != "" {
		return "binary"
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".json", ".geojson", ".har", ".ipynb":
		return "json"
	case ".jsonl", ".ndjson":
		return "jsonl"
	case ".csv":
		return "csv"
	case ".tsv", ".tab":
		return "tsv"
	case ".log", ".out", ".err", ".output", ".txt":
		if bytes.IndexByte(sample, 0) >= 0 {
			return "binary"
		}
		return "text"
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return "binary"
	}
	t := bytes.TrimLeft(bytes.TrimPrefix(sample, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		lines := nonBlankLines(t, 3)
		if len(lines) >= 2 && json.Valid(lines[0]) && json.Valid(lines[1]) {
			return "jsonl"
		}
		return "json"
	}
	if sniffDelim(sample) != 0 {
		return "csv"
	}
	return "text"
}

func nonBlankLines(b []byte, n int) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			out = append(out, bytes.TrimSpace(l))
			if len(out) == n {
				break
			}
		}
	}
	return out
}

// magic says what a binary file's first bytes show.
func magic(b []byte) string {
	for _, m := range []struct{ sig, name string }{
		{"\x89PNG", "PNG image"}, {"\xff\xd8\xff", "JPEG image"}, {"GIF8", "GIF image"},
		{"RIFF", "RIFF (WAV, WebP, AVI)"}, {"\x1f\x8b", "gzip"}, {"PK\x03\x04", "zip (also jar, docx, apk)"},
		{"\x7fELF", "ELF executable"}, {"%PDF", "PDF"}, {"SQLite format 3", "SQLite database"},
		{"OggS", "Ogg audio"}, {"fLaC", "FLAC audio"}, {"glTF", "glTF binary (GLB)"}, {"\x28\xb5\x2f\xfd", "zstd"},
	} {
		if bytes.HasPrefix(b, []byte(m.sig)) {
			return ", " + m.name
		}
	}
	return ""
}

// runJSON shows a JSON document's shape, or what a path selects in it.
func runJSON(w io.Writer, name string, r io.Reader, o options, size int64, next *[]string) int {
	data, err := io.ReadAll(r)
	if err != nil {
		fmt.Fprintf(w, "peek: %v\n", err)
		return 2
	}
	if size < 0 {
		size = int64(len(data))
	}
	root, err := parseJSON(data)
	if err != nil {
		fmt.Fprintf(w, "peek: %s  not valid JSON: %v\n", name, err)
		return 2
	}
	if o.arg == "" {
		fmt.Fprintf(w, "peek: %s  JSON, %s, %s\n", name, sizeText(size), describe(root))
		shapeCard(w, name, []*node{root}, "", o.budget-1, next)
		return 0
	}
	steps, err := parsePath(o.arg)
	if err != nil {
		fmt.Fprintf(w, "peek: %v\n", err)
		return 2
	}
	got, why := selectPath([]*node{root}, steps)
	return showSelection(w, name, formatPath(steps), got, why, o.budget, next)
}

// describe is what a value is: "object with 4 keys", "array of 12 values".
func describe(n *node) string {
	switch n.kind {
	case kObject:
		return fmt.Sprintf("object with %s", plural(len(n.keys), "key"))
	case kArray:
		return fmt.Sprintf("array of %s", plural(len(n.vals), "value"))
	}
	return kindName(n)
}

// shapeCard writes the paths of the values' merged shape under path.
func shapeCard(w io.Writer, name string, vals []*node, path string, budget int, next *[]string) {
	st := newStat()
	for _, v := range vals {
		st.add(v)
	}
	shapeOf(w, name, st, path, budget, next)
}

func shapeOf(w io.Writer, name string, st *stat, path string, budget int, next *[]string) {
	var all []row
	rows(st, path, 0, 0, &all)
	if len(all) > 1 && st.onlyContainers() {
		all = all[1:] // the header said what the top is
	}
	room := budget
	if budget > 0 {
		room = max(budget-1, 1)
	}
	lines, hidden := card(all, room)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	if hidden > 0 {
		fmt.Fprintf(w, "… %s deeper (a path shows one part, -b 0 all)\n", plural(hidden, "more path"))
	}
	best := -1 // the shallowest array of objects: its first element is a good look
	for i, rw := range all {
		if strings.HasPrefix(rw.typ, "array of") && strings.HasSuffix(rw.typ, "objects") && (best < 0 || rw.depth < all[best].depth) {
			best = i
		}
	}
	if best >= 0 {
		*next = append(*next, fmt.Sprintf("next: peek %s %s   the first element in full", shellQuote(name), shellQuote(all[best].path+"[0]")))
	}
}

// showSelection writes what a path selected.
func showSelection(w io.Writer, name, path string, got []*node, why string, budget int, next *[]string) int {
	if got == nil {
		fmt.Fprintf(w, "peek: %s %s  nothing there: %s\n", name, path, why)
		return 1
	}
	if len(got) == 1 {
		v := got[0]
		lines := pretty(v)
		if budget <= 0 || len(lines) <= budget-1 || v.kind != kObject && v.kind != kArray {
			fmt.Fprintf(w, "peek: %s %s  %s\n", name, path, describe(v))
			for _, l := range lines {
				fmt.Fprintln(w, l)
			}
			return 0
		}
		fmt.Fprintf(w, "peek: %s %s  %s, too long to print (%s): its shape\n", name, path, describe(v), plural(len(lines), "line"))
		p := path
		if p == "." {
			p = ""
		}
		shapeCard(w, name, got, p, budget-1, next)
		return 0
	}
	if allScalar(got) && len(got) <= listUpTo && (budget <= 0 || len(got) <= budget-1) {
		fmt.Fprintf(w, "peek: %s %s  %s\n", name, path, plural(len(got), "value"))
		for _, v := range got {
			fmt.Fprintln(w, scalarJSON(v, maxStr))
		}
		return 0
	}
	st := newStat()
	for _, v := range got {
		st.add(v)
	}
	fmt.Fprintf(w, "peek: %s %s  %s: %s\n", name, path, plural(len(got), "value"), st.typeLabel())
	if !st.isContainer() {
		fmt.Fprintln(w, st.detail(0))
		return 0
	}
	p := path
	if p == "." {
		p = ""
	}
	shapeOf(w, name, st, p, budget-1, next)
	return 0
}

// runJSONL reads JSON Lines one record at a time.
func runJSONL(w io.Writer, name string, r io.Reader, o options, size int64, next *[]string) int {
	var steps []step
	if o.arg != "" {
		var err error
		if steps, err = parsePath(o.arg); err != nil {
			fmt.Fprintf(w, "peek: %v\n", err)
			return 2
		}
	}
	st := newStat()
	var picked []*node
	pickedOver := false
	records, bad, firstBad, missing := 0, 0, 0, 0
	firstWhy := ""
	var read int64
	br := bufio.NewReaderSize(r, 1<<20)
	n := 0
	for {
		line, err := br.ReadBytes('\n')
		read += int64(len(line))
		if len(bytes.TrimSpace(line)) > 0 {
			n++
			rec, perr := parseJSON(line)
			if perr != nil {
				bad++
				if firstBad == 0 {
					firstBad = n
				}
			} else {
				records++
				if steps == nil && o.arg == "" {
					st.add(rec)
				} else {
					got, why := selectPath([]*node{rec}, steps)
					if got == nil {
						missing++
						if firstWhy == "" {
							firstWhy = fmt.Sprintf("record %d: %s", records, why)
						}
					}
					for _, g := range got {
						st.add(g)
						if len(picked) <= listUpTo {
							picked = append(picked, g)
						} else {
							pickedOver = true
						}
					}
				}
			}
		} else if len(line) > 0 {
			n++
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fmt.Fprintf(w, "peek: %v\n", err)
			return 2
		}
	}
	if size < 0 {
		size = read
	}
	head := fmt.Sprintf("peek: %s  JSON Lines, %s, %s", name, plural(records, "record"), sizeText(size))
	if bad > 0 {
		head += fmt.Sprintf("; %s not JSON (first: line %d)", plural(bad, "line"), firstBad)
	}
	if o.arg == "" {
		fmt.Fprintln(w, head)
		shapeOf(w, name, st, "", o.budget-1, next)
		return 0
	}
	path := formatPath(steps)
	if st.n == 0 {
		fmt.Fprintf(w, "%s\n%s: nothing there; %s\n", head, path, firstWhy)
		return 1
	}
	what := fmt.Sprintf("%s: %s from %s", path, plural(st.n, "value"), plural(records-missing, "record"))
	if missing > 0 {
		what += fmt.Sprintf(" (not in %s)", count(missing))
	}
	fmt.Fprintf(w, "%s\n%s\n", head, what)
	if !st.isContainer() {
		if !pickedOver && len(picked) <= listUpTo && len(picked) <= o.budget-2 || o.budget <= 0 && !pickedOver {
			for _, v := range picked {
				fmt.Fprintln(w, scalarJSON(v, maxStr))
			}
			return 0
		}
		fmt.Fprintf(w, "%s  %s\n", st.typeLabel(), st.detail(0))
		return 0
	}
	p := path
	if p == "." {
		p = ""
	}
	shapeOf(w, name, st, p, o.budget-2, next)
	return 0
}

// runText shows a text's card, the lines a pattern matches, or some lines.
func runText(w io.Writer, name string, r io.Reader, o options, next *[]string) int {
	t := &textScan{tailN: 10, keepAll: max(o.budget-1, 0)}
	if o.budget <= 0 {
		t.keepAll = 1 << 30
	}
	var what string
	switch {
	case o.pattern != "":
		p := o.pattern
		if o.ignore {
			p = "(?i)" + p
		}
		re, err := regexp.Compile(p)
		if err != nil {
			fmt.Fprintf(w, "peek: bad pattern: %v\n", err)
			return 2
		}
		t.match, t.keepAll = re, 0
	case o.arg != "":
		from, to, ok := lineRange(o.arg)
		if !ok {
			fmt.Fprintf(w, "peek: %q isn't a line range (like 100-200) and the file isn't JSON\n", o.arg)
			return 2
		}
		t.from, t.to = from, to
		what = fmt.Sprintf("lines %d-%d", from, to)
	case o.tail > 0:
		t.tailN, t.keepAll = o.tail, 0
	}
	if err := t.run(r); err != nil {
		fmt.Fprintf(w, "peek: %v\n", err)
		return 2
	}
	switch {
	case o.pattern != "":
		return renderHits(w, name, t, o.pattern, o.budget)
	case o.arg != "":
		return renderLines(w, name, t, what)
	case o.tail > 0:
		t.kept = t.lastLines()
		return renderLines(w, name, t, fmt.Sprintf("the last %s", plural(len(t.kept), "line")))
	}
	renderText(w, name, t, o.budget, next)
	return 0
}

// lineRange reads "100-200" or "100".
func lineRange(s string) (int, int, bool) {
	a, b, found := strings.Cut(s, "-")
	from, err := strconv.Atoi(a)
	if err != nil || from < 1 {
		return 0, 0, false
	}
	if !found {
		return from, from, true
	}
	to, err := strconv.Atoi(b)
	if err != nil || to < from {
		return 0, 0, false
	}
	return from, to, true
}

// parse reads the command line. A message with code 0 is printed as is.
func parse(args []string, stdinTTY bool) (options, int, string) {
	o := options{budget: defaultBudget}
	var pos []string
	flags := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case !flags || a == "-" || !strings.HasPrefix(a, "-"):
			pos = append(pos, a)
		case a == "--":
			flags = false
		case a == "-h" || a == "--help":
			return o, 0, helpText(wantsColor(os.Stdout))
		case a == "-V" || a == "--version":
			return o, 0, "peek " + version + "\n"
		case a == "-A":
			o.plain = true
		case a == "-i":
			o.ignore = true
		case a == "-b" || a == "-g" || a == "-t" || a == "--as":
			v, ok := value()
			if !ok {
				return o, 2, a + " needs a value"
			}
			switch a {
			case "-b", "-t":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return o, 2, fmt.Sprintf("%s takes a number, not %q", a, v)
				}
				if a == "-b" {
					o.budget = n
				} else {
					o.tail = n
				}
			case "-g":
				o.pattern = v
			case "--as":
				switch v {
				case "json", "jsonl", "csv", "tsv", "text":
					o.kind = v
				default:
					return o, 2, fmt.Sprintf("--as takes json, jsonl, csv, tsv or text, not %q", v)
				}
			}
		default:
			return o, 2, fmt.Sprintf("unknown flag %s (peek -h lists them)", a)
		}
	}
	switch {
	case len(pos) == 0 && stdinTTY:
		return o, 2, "give a file, or pipe something in (peek -h shows how)"
	case len(pos) == 0:
		o.file = "-"
	case len(pos) == 1 && !stdinTTY && looksLikePath(pos[0]):
		if _, err := os.Stat(pos[0]); err != nil {
			o.file, o.arg = "-", pos[0]
		} else {
			o.file = pos[0]
		}
	case len(pos) == 1:
		o.file = pos[0]
	case len(pos) == 2:
		o.file, o.arg = pos[0], pos[1]
	default:
		return o, 2, "one file at a time, with at most one path or line range after it"
	}
	if o.pattern != "" && o.arg != "" {
		return o, 2, "-g and a path or line range can't go together"
	}
	return o, 0, ""
}

func looksLikePath(s string) bool { return strings.HasPrefix(s, ".") || strings.HasPrefix(s, "[") }

func wantsColor(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""
}

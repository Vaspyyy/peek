package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// sniffDelim finds the delimiter that splits the first lines into the same
// number (two or more) of fields, or 0.
// sniffDelimN finds the delimiter that splits the first lines (at least
// minRows of them) into the same number, two or more, of fields: several
// rows to call unknown text a table, one for a file that is a table by its
// name, where one ragged row doesn't stop it either.
func sniffDelimN(sample []byte, minRows int) rune {
	lines := bytes.Split(sample, []byte("\n"))
	if len(lines) > 1 && len(lines[len(lines)-1]) > 0 {
		lines = lines[:len(lines)-1] // the sample may end inside a line
	}
	var rows [][]byte
	for _, l := range lines {
		if len(bytes.TrimSpace(l)) > 0 {
			rows = append(rows, l)
		}
		if len(rows) == 20 {
			break
		}
	}
	if len(rows) < minRows { // two lines with a comma each aren't a table yet
		return 0
	}
	for _, d := range []rune{'\t', ',', ';', '|'} {
		r := csv.NewReader(bytes.NewReader(bytes.Join(rows, []byte("\n"))))
		r.Comma = d
		r.LazyQuotes = true
		r.FieldsPerRecord = -1
		recs, err := r.ReadAll()
		if err != nil || len(recs) < minRows {
			continue
		}
		n := len(recs[0])
		ok := n >= 2
		for _, rec := range recs {
			ok = ok && len(rec) == n
		}
		// a column that is empty in every row ("SELECT 1;") isn't one
		for i := 0; ok && i < n; i++ {
			filled := false
			for _, rec := range recs {
				filled = filled || strings.TrimSpace(rec[i]) != ""
			}
			ok = filled
		}
		if ok {
			return d
		}
	}
	if minRows == 1 { // a .csv: the delimiter that fits most rows
		best, bestRows := rune(0), 0
		for _, d := range []rune{'\t', ',', ';', '|'} {
			r := csv.NewReader(bytes.NewReader(bytes.Join(rows, []byte("\n"))))
			r.Comma = d
			r.LazyQuotes = true
			r.FieldsPerRecord = -1
			recs, _ := r.ReadAll()
			counts := map[int]int{}
			for _, rec := range recs {
				if len(rec) >= 2 {
					counts[len(rec)]++
				}
			}
			for _, c := range counts {
				if c > bestRows {
					best, bestRows = d, c
				}
			}
		}
		if bestRows*2 > len(rows) {
			return best
		}
	}
	return 0
}

// cellNode reads a CSV cell as a JSON value: a number, a boolean, an empty
// cell (null) or a string.
func cellNode(c string) *node {
	t := strings.TrimSpace(c)
	switch {
	case t == "":
		return &node{kind: kNull}
	case t == "true" || t == "false" || t == "TRUE" || t == "FALSE" || t == "True" || t == "False":
		return &node{kind: kBool, b: strings.EqualFold(t, "true")}
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil && !strings.ContainsAny(t, "xXnN") && !leadingZero(t) {
		return &node{kind: kNumber, text: t}
	}
	return &node{kind: kString, text: c}
}

func isNumber(c string) bool { return cellNode(c).kind == kNumber }

// leadingZero: "01234" is a code (a zip, an ID), not the number 1234.
func leadingZero(t string) bool {
	t = strings.TrimLeft(t, "+-")
	return len(t) > 1 && t[0] == '0' && t[1] >= '0' && t[1] <= '9'
}

// renderTable writes the card of a CSV or TSV file.
func renderTable(w io.Writer, name string, r io.Reader, delim rune, size func() int64, budget int) error {
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	var first []string
	var sample [][]string
	var cols []*stat
	rowsN, ragged := 0, 0
	header := false
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if first == nil {
			first = rec
			header = true
			for _, c := range rec {
				if strings.TrimSpace(c) == "" || isNumber(c) {
					header = false
				}
			}
			if header {
				continue
			}
		}
		rowsN++
		if len(rec) != len(first) {
			ragged++
		}
		for len(cols) < len(rec) {
			cols = append(cols, newStat())
		}
		for i, c := range rec {
			cols[i].add(cellNode(c))
		}
		if len(sample) < 3 {
			sample = append(sample, rec)
		}
	}
	for header && len(cols) < len(first) { // a header without rows still names its columns
		cols = append(cols, newStat())
	}
	kind := "CSV"
	if delim == '\t' {
		kind = "TSV"
	} else if delim != ',' {
		kind = fmt.Sprintf("CSV (separated by %q)", delim)
	}
	fmt.Fprintf(w, "peek: %s  %s, %s, %s", name, kind, plural(rowsN, "row"), plural(len(cols), "column"))
	if header {
		fmt.Fprint(w, " (with a header)")
	}
	fmt.Fprintf(w, ", %s", sizeText(size()))
	if ragged > 0 {
		fmt.Fprintf(w, "; %s with another number of fields", plural(ragged, "row"))
	}
	fmt.Fprintln(w)
	names := make([]string, len(cols))
	for i := range cols {
		names[i] = fmt.Sprintf("column %d", i+1)
		if header && i < len(first) {
			names[i] = first[i]
		}
	}
	room := budget
	if room <= 0 {
		room = 1 << 30
	}
	room--
	nw := 0
	for _, n := range names {
		nw = max(nw, len([]rune(n)))
	}
	nw = min(nw, 30)
	tw := 0
	for _, c := range cols {
		tw = max(tw, len(colType(c)))
	}
	show := min(len(cols), max(room-5, 1))
	for i := 0; i < show; i++ {
		c := cols[i]
		d := c.detail(0)
		d = strings.ReplaceAll(d, "null ×", "empty ×")
		line := fmt.Sprintf("  %-*s  %-*s  %s", nw, clipRunes(names[i], 30), tw, colType(c), d)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		room--
	}
	if rest := len(cols) - show; rest > 0 {
		fmt.Fprintf(w, "  … %s more\n", plural(rest, "column"))
		room--
	}
	if len(sample) > 0 && room > 2 {
		fmt.Fprintln(w, "first rows")
		rowsOut := sample
		if header {
			rowsOut = append([][]string{first}, sample...)
		}
		for _, l := range alignRows(rowsOut, min(room-1, len(rowsOut))) {
			fmt.Fprintln(w, "  "+l)
		}
	}
	return nil
}

// colType names a column's kind of values, ignoring empty cells.
func colType(s *stat) string {
	var parts []string
	for k := 0; k < nKinds; k++ {
		if s.types[k] > 0 && k != kNull {
			parts = append(parts, kindWords[k][0])
		}
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, " or ")
}

// alignRows lays rows out in columns, cells clipped, at most about 150
// characters wide.
func alignRows(rows [][]string, n int) []string {
	rows = rows[:n]
	ncol := 0
	for _, r := range rows {
		ncol = max(ncol, len(r))
	}
	widths := make([]int, ncol)
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], min(len([]rune(c)), 24))
		}
	}
	total, fit := 0, 0
	for i, wd := range widths {
		if total+wd+3 > 150 && i > 0 {
			break
		}
		total += wd + 3
		fit = i + 1
	}
	var out []string
	for _, r := range rows {
		var cells []string
		for i := 0; i < fit; i++ {
			c := ""
			if i < len(r) {
				c = clipRunes(r[i], 24)
			}
			cells = append(cells, fmt.Sprintf("%-*s", widths[i], c))
		}
		line := strings.Join(cells, " | ")
		if fit < ncol {
			line += fmt.Sprintf(" | … %d more", ncol-fit)
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

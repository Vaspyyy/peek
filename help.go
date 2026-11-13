package main

import (
	"fmt"
	"strings"
)

// help colors, the same 256-color palette as the other tools' help
const (
	hTitle   = "\x1b[38;5;183;1m"
	hDim     = "\x1b[38;5;59m"
	hTagline = "\x1b[38;5;146;3m"
	hSection = "\x1b[38;5;111;1m"
	hShort   = "\x1b[38;5;151m"
	hLong    = "\x1b[38;5;117m"
	hText    = "\x1b[38;5;189m"
	hReset   = "\x1b[0m"
)

type helpFlag struct{ short, long, arg, text string }

type helpPair struct{ name, text string }

// helpBlocks says what peek shows; paragraphs are kept as written.
var helpBlocks = []string{
	`JSON: every path once, with its type and what its values look like
(arrays merged: .levels[].id covers every element). The paths are jq
paths: give one after the file to see that part (.levels[0], .a[-1],
.levels[].beats, .["a key"]).`,
	`JSON Lines: the merged shape of all records, read one at a time; a path
applies to every record. CSV and TSV: the columns, then the first rows.`,
	`Text and logs: errors and warnings first, grouped by template (the line
with its numbers and IDs as <n>), the most repeated lines, then the first
and last lines. Long lines are clipped. A short text is shown whole.`,
	`From standard input too: gh api ... | peek, or | peek .items[].name.`,
}

var helpAfter = []struct {
	title string
	paras []string
}{}

var helpSections = []struct {
	title string
	flags []helpFlag
}{
	{"TEXT", []helpFlag{
		{"-g", "", "PATTERN", "the lines matching a regular expression, grouped by template"},
		{"-i", "", "", "with -g: ignore case"},
		{"-t", "", "N", "the last N lines"},
	}},
	{"OUTPUT", []helpFlag{
		{"-b", "", "N", "about how many lines to print (default 60; 0: no limit)"},
		{"", "--as", "KIND", "read the file as json, jsonl, csv, tsv or text"},
		{"-A", "", "", "plain output (it is always plain for now)"},
		{"-h", "--help", "", "show this help"},
		{"-V", "--version", "", "show the version"},
	}},
}

var helpExamples = []helpPair{
	{"peek data.json", "the shape: every path, its type and values"},
	{"peek data.json .levels[0]", "one part, as JSON"},
	{"peek run.log", "errors, repeated lines, first and last lines"},
	{"peek run.log -g 'RUN \\d+'", "matching lines, grouped"},
	{"peek run.log 1200-1240", "those lines, clipped"},
	{"gh api repos/o/r/pulls | peek", "the shape of a command's JSON"},
}

var helpNotes = []helpPair{
	{"exit code", "0 shown, 1 a path, pattern or range matched nothing, 2 bad input"},
	{"NO_COLOR", "set it to turn colors off"},
}

// helpText renders the help, colored like the other tools' when color is true.
func helpText(color bool) string {
	paint := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return code + s + hReset
	}
	const flagWidth = 24
	var b strings.Builder
	section := func(title string) {
		fmt.Fprintf(&b, "\n  %s\n", paint(hSection, title))
	}
	pairs := func(ps []helpPair, w int, nameColor, textColor string) {
		for _, p := range ps {
			w = max(w, len(p.name))
		}
		for _, p := range ps {
			pad := strings.Repeat(" ", w-len(p.name)+2)
			fmt.Fprintf(&b, "    %s%s%s\n", paint(nameColor, p.name), pad, paint(textColor, p.text))
		}
	}

	fmt.Fprintf(&b, "\n  %s %s  %s\n", paint(hTitle, "✦ peek"), paint(hDim, "v"+version),
		paint(hTagline, "a short card about a data file or a log"))

	section("USAGE")
	fmt.Fprintf(&b, "    peek %s %s %s\n    %s peek %s %s\n", paint(hDim, "[flags]"), paint(hLong, "FILE"), paint(hDim, "[PATH | LINES]"), paint(hDim, "cmd |"), paint(hDim, "[flags]"), paint(hDim, "[PATH]"))

	prose := func(title string, paras []string) {
		section(title)
		for i, p := range paras {
			if i > 0 {
				b.WriteString("\n")
			}
			for _, line := range strings.Split(p, "\n") {
				fmt.Fprintf(&b, "    %s\n", paint(hText, line))
			}
		}
	}
	prose("WHAT IT SHOWS", helpBlocks)

	for _, s := range helpSections {
		section(s.title)
		for _, f := range s.flags {
			left, plain := "    ", "    "
			if f.short != "" {
				left = paint(hShort, f.short)
				plain = f.short
				if f.long != "" {
					left += paint(hDim, ", ")
					plain += ", "
				}
			}
			if f.long != "" {
				left += paint(hLong, f.long)
				plain += f.long
			}
			if f.arg != "" {
				left += " " + paint(hDim, f.arg)
				plain += " " + f.arg
			}
			fmt.Fprintf(&b, "    %s%s%s\n", left, strings.Repeat(" ", max(flagWidth-len(plain), 2)), paint(hText, f.text))
		}
	}

	for _, a := range helpAfter {
		prose(a.title, a.paras)
	}
	section("EXAMPLES")
	pairs(helpExamples, flagWidth-2, hLong, hDim)
	section("GOOD TO KNOW")
	pairs(helpNotes, 0, hLong, hDim)
	b.WriteString("\n")
	return b.String()
}

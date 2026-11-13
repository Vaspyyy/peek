package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Kinds of JSON values.
const (
	kObject = iota
	kArray
	kString
	kNumber
	kBool
	kNull
	nKinds
)

// node is a JSON value that keeps object keys in the order of the file.
type node struct {
	kind int
	keys []string // object keys, parallel to vals
	vals []*node  // object values or array elements
	text string   // a string's value, a number's text
	b    bool
}

// decodeOne reads one JSON value from dec.
func decodeOne(dec *json.Decoder) (*node, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeFrom(dec, t)
}

func decodeFrom(dec *json.Decoder, t json.Token) (*node, error) {
	switch v := t.(type) {
	case json.Delim:
		switch v {
		case '{':
			n := &node{kind: kObject}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, errors.New("object key isn't a string")
				}
				val, err := decodeOne(dec)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, key)
				n.vals = append(n.vals, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		case '[':
			n := &node{kind: kArray}
			for dec.More() {
				val, err := decodeOne(dec)
				if err != nil {
					return nil, err
				}
				n.vals = append(n.vals, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		}
		return nil, fmt.Errorf("unexpected %v", v)
	case string:
		return &node{kind: kString, text: v}, nil
	case json.Number:
		return &node{kind: kNumber, text: string(v)}, nil
	case bool:
		return &node{kind: kBool, b: v}, nil
	case nil:
		return &node{kind: kNull}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", t)
}

// errMulti: the data has more than one JSON value, one after another.
var errMulti = errors.New("more than one JSON value")

// parseJSON reads data as exactly one JSON document.
func parseJSON(data []byte) (*node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	n, err := decodeOne(dec)
	if err != nil {
		return nil, jsonError(data, dec, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w (at byte %d)", errMulti, dec.InputOffset())
	}
	return n, nil
}

// jsonError adds a line and column to a decoding error.
func jsonError(data []byte, dec *json.Decoder, err error) error {
	off := int(dec.InputOffset())
	var se *json.SyntaxError
	if errors.As(err, &se) {
		off = int(se.Offset)
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return fmt.Errorf("the JSON ends early (at byte %d)", len(data))
	}
	line, col := lineCol(data, off)
	return fmt.Errorf("line %d, column %d: %v", line, col, err)
}

func lineCol(data []byte, off int) (int, int) {
	off = min(max(off, 0), len(data))
	line := bytes.Count(data[:off], []byte("\n")) + 1
	return line, off - bytes.LastIndexByte(data[:off], '\n')
}

// A step of a path: a key, an index, or every element.
type step struct {
	key   string
	index int
	kind  int // 0 key, 1 index, 2 every element
}

// parsePath reads a jq path: ".", ".a.b", ".a[0]", ".a[-1]", ".a[]",
// `.["a key"]`, `."a key"`.
func parsePath(p string) ([]step, error) {
	if p == "" || p[0] != '.' && p[0] != '[' {
		return nil, fmt.Errorf("a path starts with . (like .items[0].name)")
	}
	var steps []step
	i := 0
	for i < len(p) {
		switch {
		case p[i] == '.':
			i++
			if i == len(p) {
				if len(steps) == 0 {
					return nil, nil // "." is the whole document
				}
				return nil, fmt.Errorf("%q ends with a dot", p)
			}
			switch {
			case p[i] == '[':
				continue
			case p[i] == '"':
				s, n, err := quoted(p[i:])
				if err != nil {
					return nil, err
				}
				steps = append(steps, step{key: s})
				i += n
			default:
				j := i
				for j < len(p) && (p[j] == '_' || p[j] == '-' || p[j] >= 0x80 || unicode.IsLetter(rune(p[j])) || unicode.IsDigit(rune(p[j]))) {
					j++
				}
				if j == i {
					return nil, fmt.Errorf("%q: expected a key after the dot at %d", p, i)
				}
				steps = append(steps, step{key: p[i:j]})
				i = j
			}
		case p[i] == '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("%q: [ without ]", p)
			}
			in := p[i+1 : i+end]
			switch {
			case in == "":
				steps = append(steps, step{kind: 2})
				i += end + 1
			case in[0] == '"':
				s, n, err := quoted(p[i+1:])
				if err != nil {
					return nil, err
				}
				if i+1+n >= len(p) || p[i+1+n] != ']' {
					return nil, fmt.Errorf("%q: expected ] after the quoted key", p)
				}
				steps = append(steps, step{key: s})
				i += 1 + n + 1
			default:
				n, err := strconv.Atoi(in)
				if err != nil {
					return nil, fmt.Errorf("%q: [%s] isn't an index (a number, or [] for every element)", p, in)
				}
				steps = append(steps, step{index: n, kind: 1})
				i += end + 1
			}
		default:
			return nil, fmt.Errorf("%q: unexpected %q at %d", p, p[i], i)
		}
	}
	return steps, nil
}

// quoted reads a JSON string at the start of s and says how long it was.
func quoted(s string) (string, int, error) {
	for j := 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '"' {
			var out string
			if err := json.Unmarshal([]byte(s[:j+1]), &out); err != nil {
				return "", 0, err
			}
			return out, j + 1, nil
		}
	}
	return "", 0, fmt.Errorf("unclosed quote in %q", s)
}

// formatPath writes steps back as a jq path.
func formatPath(steps []step) string {
	var b strings.Builder
	for _, s := range steps {
		switch s.kind {
		case 0:
			b.WriteString(keySeg(s.key, b.Len() == 0))
		case 1:
			if b.Len() == 0 {
				b.WriteByte('.')
			}
			fmt.Fprintf(&b, "[%d]", s.index)
		default:
			if b.Len() == 0 {
				b.WriteByte('.')
			}
			b.WriteString("[]")
		}
	}
	if b.Len() == 0 {
		return "."
	}
	return b.String()
}

// keySeg is a key as a path segment: .name, or ["a key"] (.["a key"] first).
func keySeg(k string, first bool) string {
	ident := k != ""
	for i, r := range k {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			ident = false
			break
		}
	}
	if ident {
		return "." + k
	}
	q := jsonQuote(k)
	if first {
		return ".[" + q + "]"
	}
	return "[" + q + "]"
}

// selectPath applies steps to roots. It returns the values reached, or why
// none were: where the path stopped.
func selectPath(roots []*node, steps []step) ([]*node, string, int) {
	cur := roots
	missed := 0 // values along the way that the next step found nothing in
	for k, s := range steps {
		var next []*node
		var why string
		before := 0
		for _, n := range cur {
			before = len(next)
			switch s.kind {
			case 0:
				if n.kind != kObject {
					why = fmt.Sprintf("%s is %s, not an object", formatPath(steps[:k]), kindName(n))
					break
				}
				found := -1 // the last one, as in jq, when a key repeats
				for i, key := range n.keys {
					if key == s.key {
						found = i
					}
				}
				if found >= 0 {
					next = append(next, n.vals[found])
				} else {
					why = fmt.Sprintf("no key %q in %s; its keys: %s", s.key, formatPath(steps[:k]), keyList(n.keys, 12))
				}
			case 1:
				if n.kind != kArray {
					why = fmt.Sprintf("%s is %s, not an array", formatPath(steps[:k]), kindName(n))
					break
				}
				i := s.index
				if i < 0 {
					i += len(n.vals)
				}
				if i < 0 || i >= len(n.vals) {
					why = fmt.Sprintf("%s has %s", formatPath(steps[:k]), plural(len(n.vals), "element"))
					break
				}
				next = append(next, n.vals[i])
			default:
				if n.kind != kArray && n.kind != kObject {
					why = fmt.Sprintf("%s is %s, which [] can't go into", formatPath(steps[:k]), kindName(n))
					break
				}
				next = append(next, n.vals...)
				before = -1 // an empty array isn't a miss
			}
			if len(next) == before {
				missed++
			}
		}
		if len(next) == 0 {
			if why == "" {
				why = fmt.Sprintf("%s is empty", formatPath(steps[:k]))
			}
			return nil, why, missed
		}
		cur = next
	}
	return cur, "", missed
}

func keyList(keys []string, n int) string {
	if len(keys) <= n {
		return strings.Join(keys, ", ")
	}
	return strings.Join(keys[:n], ", ") + fmt.Sprintf(", … %d more", len(keys)-n)
}

func kindName(n *node) string {
	switch n.kind {
	case kObject:
		return "an object"
	case kArray:
		return "an array"
	case kString:
		return "a string"
	case kNumber:
		return "a number"
	case kBool:
		return "a boolean"
	}
	return "null"
}

// Limits for printing a value as JSON; run lifts them for -b 0.
const (
	defStr   = 200 // characters of a string
	defElems = 20  // elements of an array before it's cut to its first 10
	defKeys  = 60  // keys of an object before it's cut to its first 40
)

var maxStr, maxElems, maxKeys = defStr, defElems, defKeys

// jsonQuote writes s as a JSON string, without escaping & < > as Go's
// encoder does for HTML.
func jsonQuote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// pretty writes n as lines of indented JSON, with long strings clipped and
// long arrays and objects cut.
func pretty(n *node) []string {
	var lines []string
	var cur strings.Builder
	flush := func() {
		lines = append(lines, cur.String())
		cur.Reset()
	}
	var walk func(n *node, ind string)
	walk = func(n *node, ind string) {
		switch n.kind {
		case kObject:
			if len(n.keys) == 0 {
				cur.WriteString("{}")
				return
			}
			cur.WriteString("{")
			flush()
			keep := lastKeys(n.keys) // a repeated key shows its last value, as in jq
			var idx []int
			for i := range n.keys {
				if keep[i] {
					idx = append(idx, i)
				}
			}
			show := len(idx)
			if show > maxKeys {
				show = maxKeys * 2 / 3
			}
			for j, i := range idx[:show] {
				cur.WriteString(ind + "  " + jsonQuote(n.keys[i]) + ": ")
				walk(n.vals[i], ind+"  ")
				if j < len(idx)-1 {
					cur.WriteString(",")
				}
				flush()
			}
			if show < len(idx) {
				cur.WriteString(fmt.Sprintf("%s  … %d more keys", ind, len(idx)-show))
				flush()
			}
			cur.WriteString(ind + "}")
		case kArray:
			if len(n.vals) == 0 {
				cur.WriteString("[]")
				return
			}
			if allScalar(n.vals) && len(n.vals) <= maxElems {
				if line := inline(n.vals); len(ind)+len(line) <= 100 {
					cur.WriteString(line)
					return
				}
			}
			cur.WriteString("[")
			flush()
			show := len(n.vals)
			if show > maxElems {
				show = maxElems / 2
			}
			for i := 0; i < show; i++ {
				cur.WriteString(ind + "  ")
				walk(n.vals[i], ind+"  ")
				if i < len(n.vals)-1 {
					cur.WriteString(",")
				}
				flush()
			}
			if show < len(n.vals) {
				cur.WriteString(fmt.Sprintf("%s  … %d more", ind, len(n.vals)-show))
				flush()
			}
			cur.WriteString(ind + "]")
		default:
			cur.WriteString(scalarJSON(n, maxStr))
		}
	}
	walk(n, "")
	flush()
	return lines
}

func allScalar(ns []*node) bool {
	for _, n := range ns {
		if n.kind == kObject || n.kind == kArray {
			return false
		}
	}
	return true
}

func inline(ns []*node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = scalarJSON(n, 40)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// scalarJSON writes a scalar as JSON, a string clipped to n characters.
func scalarJSON(n *node, limit int) string {
	switch n.kind {
	case kString:
		s := n.text
		extra := ""
		if r := []rune(s); len(r) > limit {
			s = string(r[:limit])
			extra = fmt.Sprintf(" … +%s chars", count(len(r)-limit))
		}
		return jsonQuote(s) + extra
	case kNumber:
		return n.text
	case kBool:
		return strconv.FormatBool(n.b)
	}
	return "null"
}

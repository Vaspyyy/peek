package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits for collecting statistics.
const (
	strCap    = 200 // distinct strings counted per path; past it, "many"
	strKey    = 80  // characters of a string kept for counting it
	mapAt     = 40  // an object with more keys than this is read as a map
	listUpTo  = 40  // values listed one per line when a path selects several
	topValues = 6   // repeated values counted on a line
)

// stat is what the values at one path look like.
type stat struct {
	n     int // values seen
	types [nKinds]int

	// objects
	objs   int
	keys   []string // in the order they were first seen
	fields map[string]*stat

	// objects read as maps (more than mapAt keys): their values merged
	maps     int
	mapVals  *stat
	mapKeys  []string // a few example keys
	mapMin   int
	mapMax   int
	mapTotal int

	// arrays
	arrs           int
	elem           *stat
	minLen, maxLen int

	// strings
	strs    map[string]int
	strOver bool
	order   []string // distinct strings in the order seen (up to strCap)

	// numbers
	nums     int
	min, max float64
	allInt   bool
	numTexts map[string]int // up to topValues+1 distinct texts

	// booleans
	t, f int
}

func newStat() *stat { return &stat{} }

// add takes one more value at this path.
func (s *stat) add(n *node) {
	s.n++
	s.types[n.kind]++
	switch n.kind {
	case kObject:
		if len(n.keys) > mapAt {
			s.addMap(n)
			return
		}
		s.objs++
		if s.fields == nil {
			s.fields = map[string]*stat{}
		}
		for i, k := range n.keys {
			f := s.fields[k]
			if f == nil {
				f = newStat()
				s.fields[k] = f
				s.keys = append(s.keys, k)
			}
			f.add(n.vals[i])
		}
	case kArray:
		if s.arrs == 0 || len(n.vals) < s.minLen {
			s.minLen = len(n.vals)
		}
		s.maxLen = max(s.maxLen, len(n.vals))
		s.arrs++
		if s.elem == nil {
			s.elem = newStat()
		}
		for _, v := range n.vals {
			s.elem.add(v)
		}
	case kString:
		if s.strs == nil {
			s.strs = map[string]int{}
		}
		key := clipRunes(n.text, strKey) // long strings are counted by their start
		if _, ok := s.strs[key]; ok {
			s.strs[key]++
		} else if len(s.strs) < strCap {
			s.strs[key] = 1
			s.order = append(s.order, key)
		} else {
			s.strOver = true
		}
	case kNumber:
		v, _ := strconv.ParseFloat(n.text, 64)
		isInt := !strings.ContainsAny(n.text, ".eE")
		if s.nums == 0 {
			s.min, s.max, s.allInt = v, v, isInt
			s.numTexts = map[string]int{}
		} else {
			s.min, s.max = math.Min(s.min, v), math.Max(s.max, v)
			s.allInt = s.allInt && isInt
		}
		s.nums++
		if _, ok := s.numTexts[n.text]; ok || len(s.numTexts) <= topValues {
			s.numTexts[n.text]++
		}
	case kBool:
		if n.b {
			s.t++
		} else {
			s.f++
		}
	}
}

func (s *stat) addMap(n *node) {
	if s.maps == 0 || len(n.keys) < s.mapMin {
		s.mapMin = len(n.keys)
	}
	s.mapMax = max(s.mapMax, len(n.keys))
	s.maps++
	s.mapTotal += len(n.keys)
	if s.mapVals == nil {
		s.mapVals = newStat()
	}
	for i, k := range n.keys {
		if len(s.mapKeys) < 3 {
			s.mapKeys = append(s.mapKeys, clipRunes(k, strKey))
		}
		s.mapVals.add(n.vals[i])
	}
}

// row is one line of a shape card.
type row struct {
	path, typ, detail string
	depth             int
}

// rows lists the paths under s, depth first, in the order keys were seen.
func rows(s *stat, path string, depth int, parentObjs int, out *[]row) {
	*out = append(*out, row{path: pathOr(path), typ: s.typeLabel(), detail: s.detail(parentObjs), depth: depth})
	if s.objs > 0 {
		for _, k := range s.keys {
			rows(s.fields[k], path+keySeg(k, path == ""), depth+1, s.objs, out)
		}
	}
	if s.maps > 0 {
		p := path + "[]"
		if path == "" {
			p = ".[]"
		}
		rows(s.mapVals, p, depth+1, 0, out)
	}
	if s.elem != nil && s.elem.n > 0 && s.elem.isContainer() {
		p := path + "[]"
		if path == "" {
			p = ".[]"
		}
		// the elements' own line says nothing the array's doesn't: go
		// straight to what's in them
		e := s.elem
		start := len(*out)
		rows(e, p, depth+1, 0, out)
		if e.onlyContainers() && len(*out) > start {
			*out = append((*out)[:start], (*out)[start+1:]...)
		}
	}
}

func pathOr(p string) string {
	if p == "" {
		return "."
	}
	return p
}

func (s *stat) isContainer() bool { return s.types[kObject] > 0 || s.types[kArray] > 0 }

// onlyContainers: every value is an object (not a map) or every value is an
// array, so the line for it would only repeat its parent's.
func (s *stat) onlyContainers() bool {
	return s.types[kObject] == s.n && s.maps == 0 || s.types[kArray] == s.n
}

var kindWords = [nKinds][2]string{
	{"object", "objects"}, {"array", "arrays"}, {"string", "strings"},
	{"number", "numbers"}, {"boolean", "booleans"}, {"null", "nulls"},
}

// typeLabel says what kinds of values are here: "string", "string or null",
// "array of 12 objects".
func (s *stat) typeLabel() string {
	var parts []string
	for k := 0; k < nKinds; k++ {
		if s.types[k] == 0 {
			continue
		}
		switch k {
		case kArray:
			parts = append(parts, s.arrayLabel())
		default:
			parts = append(parts, kindWords[k][0])
		}
	}
	return strings.Join(parts, " or ")
}

func (s *stat) arrayLabel() string {
	if s.maxLen == 0 {
		return "empty array"
	}
	what := "values"
	if e := s.elem; e != nil {
		kinds := 0
		last := 0
		for k := 0; k < nKinds; k++ {
			if e.types[k] > 0 {
				kinds++
				last = k
			}
		}
		if kinds == 1 {
			what = kindWords[last][1]
		}
	}
	n := strconv.Itoa(s.maxLen)
	if s.arrs > 1 && s.minLen != s.maxLen {
		n = fmt.Sprintf("%d to %d", s.minLen, s.maxLen)
	}
	if n == "1" {
		what = strings.TrimSuffix(what, "s")
	}
	return fmt.Sprintf("array of %s %s", n, what)
}

// detail says what the values look like, and how often the path is there
// when parentObjs objects could have had it.
func (s *stat) detail(parentObjs int) string {
	var parts []string
	if parentObjs > 0 && s.n < parentObjs {
		parts = append(parts, fmt.Sprintf("in %s of %s", count(s.n), count(parentObjs)))
	}
	if s.types[kString] > 0 {
		parts = append(parts, s.strDetail())
	}
	if s.nums > 0 {
		parts = append(parts, s.numDetail())
	}
	if s.t+s.f > 0 {
		switch {
		case s.f == 0 && s.t == 1:
			parts = append(parts, "true")
		case s.t == 0 && s.f == 1:
			parts = append(parts, "false")
		case s.f == 0:
			parts = append(parts, "true ×"+count(s.t))
		case s.t == 0:
			parts = append(parts, "false ×"+count(s.f))
		default:
			parts = append(parts, fmt.Sprintf("true ×%s, false ×%s", count(s.t), count(s.f)))
		}
	}
	if s.types[kNull] > 0 && s.types[kNull] < s.n {
		parts = append(parts, fmt.Sprintf("null ×%s", count(s.types[kNull])))
	}
	if s.maps > 0 {
		keys := fmt.Sprintf("%s keys", count(s.mapMax))
		if s.maps > 1 && s.mapMin != s.mapMax {
			keys = fmt.Sprintf("%s to %s keys", count(s.mapMin), count(s.mapMax))
		}
		parts = append(parts, fmt.Sprintf("%s like %s", keys, quoteList(s.mapKeys)))
	}
	if s.elem != nil && s.elem.n > 0 && !s.elem.isContainer() {
		parts = append(parts, s.elem.detail(0))
	}
	return strings.Join(nonEmpty(parts), "; ")
}

func nonEmpty(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// strDetail: one value as is, a few repeated ones counted, many sampled.
func (s *stat) strDetail() string {
	n := s.types[kString]
	distinct := len(s.strs)
	switch {
	case distinct == 1 && !s.strOver:
		v := quote(s.order[0], 60)
		if n > 1 {
			v += " ×" + count(n)
		}
		return v
	case !s.strOver && distinct <= topValues && distinct < n:
		return s.topStrings(topValues)
	case !s.strOver && distinct <= 3:
		return quoteList(s.order)
	}
	many := count(distinct) + " distinct"
	if s.strOver {
		many = fmt.Sprintf("over %s distinct", count(strCap))
	}
	if top := s.topStrings(3); distinct < n && s.repeated(3) {
		return many + ", most often " + top
	}
	return many + ", like " + quoteList(s.order[:min(2, len(s.order))])
}

// repeated tells whether the k most common strings each occur more than once.
func (s *stat) repeated(k int) bool {
	c := s.byCount()
	return len(c) >= k && s.strs[c[k-1]] > 1
}

func (s *stat) byCount() []string {
	vs := append([]string(nil), s.order...)
	sort.SliceStable(vs, func(i, j int) bool { return s.strs[vs[i]] > s.strs[vs[j]] })
	return vs
}

func (s *stat) topStrings(k int) string {
	vs := s.byCount()
	var parts []string
	for i, v := range vs {
		if i == k {
			parts = append(parts, fmt.Sprintf("… %d more", len(vs)-k))
			break
		}
		parts = append(parts, fmt.Sprintf("%s ×%s", quote(v, 30), count(s.strs[v])))
	}
	return strings.Join(parts, ", ")
}

func (s *stat) numDetail() string {
	if len(s.numTexts) == 1 {
		for t, c := range s.numTexts {
			if c > 1 {
				return t + " ×" + count(c)
			}
			return t
		}
	}
	if len(s.numTexts) <= 3 && s.nums > len(s.numTexts) {
		var ts []string
		for t := range s.numTexts {
			ts = append(ts, t)
		}
		sort.Slice(ts, func(i, j int) bool { return s.numTexts[ts[i]] > s.numTexts[ts[j]] })
		var parts []string
		for _, t := range ts {
			parts = append(parts, fmt.Sprintf("%s ×%s", t, count(s.numTexts[t])))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s to %s", num(s.min, s.allInt), num(s.max, s.allInt))
}

func num(v float64, isInt bool) string {
	if isInt && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', 6, 64)
}

func quote(s string, n int) string {
	q := strconv.Quote(clipRunes(s, n))
	return q
}

func quoteList(ss []string) string {
	var parts []string
	for _, s := range ss {
		parts = append(parts, quote(s, 30))
	}
	return strings.Join(parts, ", ")
}

func clipRunes(s string, n int) string {
	if len(s) <= n { // no more bytes than runes allowed
		return s
	}
	runes := 0
	for i := range s {
		if runes == n-1 {
			if utf8.RuneCountInString(s[i:]) <= 1 {
				return s
			}
			return s[:i] + "…"
		}
		runes++
	}
	return s
}

// card renders rows within a budget: the deepest levels go first, and the
// last line says what was left out. budget 0 means no limit.
func card(all []row, budget int) (lines []string, hidden int) {
	shown := all
	if budget > 0 && len(all) > budget {
		maxDepth, minDepth := 0, all[0].depth
		for _, r := range all {
			maxDepth = max(maxDepth, r.depth)
			minDepth = min(minDepth, r.depth)
		}
		for d := maxDepth; d >= minDepth; d-- {
			shown = shown[:0:0]
			for _, r := range all {
				if r.depth <= d {
					shown = append(shown, r)
				}
			}
			if len(shown) <= budget || d == minDepth {
				break
			}
		}
		if len(shown) > budget {
			shown = shown[:budget]
		}
		hidden = len(all) - len(shown)
	}
	w := 0
	for _, r := range shown {
		w = max(w, len(r.path))
	}
	w = min(w, 48)
	tw := 0
	for _, r := range shown {
		tw = max(tw, len(r.typ))
	}
	tw = min(tw, 28)
	for _, r := range shown {
		line := fmt.Sprintf("%-*s  %-*s", w, r.path, tw, r.typ)
		if r.detail != "" {
			line += "  " + r.detail
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines, hidden
}

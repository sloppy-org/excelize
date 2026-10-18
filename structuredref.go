// Copyright 2016 - 2026 The excelize Authors. All rights reserved. Use of
// this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package excelize

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// structTable is the part of a table definition that structured references
// resolve against.
type structTable struct {
	sheet                    string
	name                     string
	top, left, bottom, right int
	headerRows, totalsRows   int
	columns                  []string
	headerTexts              []string
}

var xmlEscapeRe = regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`)

// decodeXMLEscapes turns the OOXML "_xHHHH_" escapes of table column names into
// the characters they stand for.
func decodeXMLEscapes(s string) string {
	return xmlEscapeRe.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.ParseUint(m[2:6], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
}

// looseColumnKey drops control characters (tab, newline, ...) so a column name
// still matches when the header cell and the table definition disagree on them.
func looseColumnKey(s string) string {
	s = decodeXMLEscapes(s)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}

// loadStructTables reads every table of the workbook with its column names.
func (f *File) loadStructTables() []structTable {
	var out []structTable
	for _, sheet := range f.GetSheetList() {
		ws, err := f.workSheetReader(sheet)
		if err != nil || ws.TableParts == nil {
			continue
		}
		for _, part := range ws.TableParts.TableParts {
			if part == nil {
				continue
			}
			target := f.getSheetRelationshipsTargetByID(sheet, part.RID)
			tableXML := strings.TrimPrefix(strings.ReplaceAll(target, "..", "xl"), "/")
			content, ok := f.Pkg.Load(tableXML)
			if !ok {
				continue
			}
			var t xlsxTable
			if err := f.xmlNewDecoder(bytes.NewReader(namespaceStrictToTransitional(content.([]byte)))).
				Decode(&t); err != nil && err != io.EOF {
				continue
			}
			coords, err := rangeRefToCoordinates(t.Ref)
			if err != nil {
				continue
			}
			_ = sortCoordinates(coords)
			st := structTable{
				sheet: sheet, name: t.DisplayName,
				left: coords[0], top: coords[1], right: coords[2], bottom: coords[3],
				headerRows: 1, totalsRows: t.TotalsRowCount,
			}
			if st.name == "" {
				st.name = t.Name
			}
			if t.HeaderRowCount != nil {
				st.headerRows = *t.HeaderRowCount
			}
			if t.TableColumns != nil {
				for _, c := range t.TableColumns.TableColumn {
					st.columns = append(st.columns, c.Name)
				}
			}
			for col := st.left; col <= st.right; col++ {
				text := ""
				if st.headerRows > 0 {
					if name, err := CoordinatesToCellName(col, st.top); err == nil {
						text, _ = f.GetCellValue(sheet, name)
					}
				}
				st.headerTexts = append(st.headerTexts, text)
			}
			out = append(out, st)
		}
	}
	return out
}

// columnIndex returns the 0-based column of the table for a header name.
func (t *structTable) columnIndex(name string) int {
	for i, c := range t.columns {
		if c == name || decodeXMLEscapes(c) == name {
			return i
		}
	}
	for i, c := range t.headerTexts {
		if c == name {
			return i
		}
	}
	key, found := strings.TrimSpace(looseColumnKey(name)), -1
	for i := 0; i < t.right-t.left+1; i++ {
		var names []string
		if i < len(t.columns) {
			names = append(names, t.columns[i])
		}
		if i < len(t.headerTexts) {
			names = append(names, t.headerTexts[i])
		}
		for _, n := range names {
			if strings.TrimSpace(looseColumnKey(n)) == key {
				if found >= 0 && found != i {
					return -1
				}
				found = i
			}
		}
	}
	return found
}

// splitStructuredSpec splits the text between the outer brackets of a
// structured reference into its items. "[a],[b]" gives {"a", "b"}; a column
// range "[a]:[b]" gives one item "a:b" with the range flag set. "'" escapes the
// next character.
type structItem struct {
	text    string
	colTo   string
	isRange bool
}

func splitStructuredSpec(spec string) ([]structItem, error) {
	// A plain column name keeps its blanks: header text may end in a space.
	if !strings.HasPrefix(strings.TrimLeft(spec, " "), "[") {
		return []structItem{{text: unescapeStructured(spec)}}, nil
	}
	spec = strings.TrimSpace(spec)
	var items []structItem
	rs, i := []rune(spec), 0
	readGroup := func() (string, error) {
		if i >= len(rs) || rs[i] != '[' {
			return "", fmt.Errorf("invalid structured reference %q", spec)
		}
		i++
		var b strings.Builder
		for i < len(rs) {
			switch rs[i] {
			case '\'':
				if i+1 < len(rs) {
					b.WriteRune(rs[i+1])
					i += 2
					continue
				}
				return "", fmt.Errorf("invalid structured reference %q", spec)
			case ']':
				i++
				return b.String(), nil
			case '[':
				return "", fmt.Errorf("invalid structured reference %q", spec)
			}
			b.WriteRune(rs[i])
			i++
		}
		return "", fmt.Errorf("invalid structured reference %q", spec)
	}
	for i < len(rs) {
		for i < len(rs) && rs[i] == ' ' {
			i++
		}
		first, err := readGroup()
		if err != nil {
			return nil, err
		}
		item := structItem{text: first}
		if i < len(rs) && rs[i] == ':' {
			i++
			second, err := readGroup()
			if err != nil {
				return nil, err
			}
			item.isRange, item.colTo = true, second
		}
		items = append(items, item)
		for i < len(rs) && rs[i] == ' ' {
			i++
		}
		if i < len(rs) {
			if rs[i] != ',' {
				return nil, fmt.Errorf("invalid structured reference %q", spec)
			}
			i++
		}
	}
	return items, nil
}

func unescapeStructured(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\'' && i+1 < len(rs) {
			i++
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

// resolve turns table[spec] into an absolute A1 reference. row is
// the row of the formula cell, used for [#This Row] and the @ shorthand.
func (t *structTable) resolve(spec string, row int) (string, error) {
	items, err := splitStructuredSpec(spec)
	if err != nil {
		return "", err
	}
	dataTop, dataBottom := t.top+t.headerRows, t.bottom-t.totalsRows
	rowFrom, rowTo, haveRows := 0, 0, false
	addRows := func(from, to int) {
		if !haveRows || from < rowFrom {
			rowFrom = from
		}
		if !haveRows || to > rowTo {
			rowTo = to
		}
		haveRows = true
	}
	colFrom, colTo, haveCols := 0, 0, false
	addCols := func(from, to int) {
		if from > to {
			from, to = to, from
		}
		if !haveCols || from < colFrom {
			colFrom = from
		}
		if !haveCols || to > colTo {
			colTo = to
		}
		haveCols = true
	}
	col := func(name string) (int, error) {
		idx := t.columnIndex(name)
		if idx < 0 {
			return 0, fmt.Errorf("table %s has no column %q", t.name, name)
		}
		return t.left + idx, nil
	}
	for _, item := range items {
		text := item.text
		switch {
		case strings.HasPrefix(text, "#") && !item.isRange:
			switch strings.ToLower(text) {
			case "#all":
				addRows(t.top, t.bottom)
			case "#data":
				addRows(dataTop, dataBottom)
			case "#headers":
				addRows(t.top, t.top+t.headerRows-1)
			case "#totals":
				addRows(dataBottom+1, t.bottom)
			case "#this row":
				addRows(row, row)
			default:
				return "", fmt.Errorf("unknown table item %q", text)
			}
		case strings.HasPrefix(text, "@"):
			addRows(row, row)
			name := strings.TrimLeft(text[1:], " ")
			if name != "" {
				c, err := col(name)
				if err != nil {
					return "", err
				}
				addCols(c, c)
			}
		default:
			from, err := col(text)
			if err != nil {
				return "", err
			}
			to := from
			if item.isRange {
				if to, err = col(item.colTo); err != nil {
					return "", err
				}
			}
			addCols(from, to)
		}
	}
	if !haveRows {
		addRows(dataTop, dataBottom)
	}
	if !haveCols {
		addCols(t.left, t.right)
	}
	if rowFrom < t.top || rowTo > t.bottom || rowFrom > rowTo {
		return "", fmt.Errorf("structured reference rows outside table %s", t.name)
	}
	from, err := CoordinatesToCellName(colFrom, rowFrom, true)
	if err != nil {
		return "", err
	}
	to, err := CoordinatesToCellName(colTo, rowTo, true)
	if err != nil {
		return "", err
	}
	return "'" + strings.ReplaceAll(t.sheet, "'", "''") + "'!" + from + ":" + to, nil
}

// expandFormulaText prepares a formula for the tokenizer. Outside string
// literals it replaces line breaks and tabs, which Excel treats as blanks, by
// spaces, and it rewrites structured table references (Table1[Column],
// Table1[[#This Row],[Column]], [@Column], ...) to absolute A1 ranges. Header
// names may contain newlines and tabs; they are matched as written.
func (f *File) expandFormulaText(sheet, cell, formula string) string {
	if !strings.ContainsAny(formula, "[\n\r\t") {
		return formula
	}
	var (
		out    strings.Builder
		rs     = []rune(formula)
		tables []structTable
		loaded bool
		row    int
	)
	if _, r, err := CellNameToCoordinates(cell); err == nil {
		row = r
	}
	isName := func(r rune) bool {
		return r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f
	}
	for i := 0; i < len(rs); i++ {
		ch := rs[i]
		switch {
		case ch == '"':
			j := i + 1
			for j < len(rs) {
				if rs[j] == '"' {
					if j+1 < len(rs) && rs[j+1] == '"' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(rs) {
				j = len(rs) - 1
			}
			out.WriteString(string(rs[i : j+1]))
			i = j
		case ch == '\n' || ch == '\r' || ch == '\t':
			out.WriteRune(' ')
		case ch == '[':
			end := matchStructuredBracket(rs, i)
			if end < 0 {
				out.WriteRune(ch)
				continue
			}
			// the table name is the identifier already copied to out.
			text := out.String()
			k := len(text)
			for k > 0 && isName(rune(text[k-1])) {
				k--
			}
			name := text[k:]
			if !loaded {
				tables, loaded = f.loadStructTables(), true
			}
			var tbl *structTable
			for ti := range tables {
				if name != "" && strings.EqualFold(tables[ti].name, name) {
					tbl = &tables[ti]
					break
				}
				if name == "" && tables[ti].sheet == sheet && row >= tables[ti].top && row <= tables[ti].bottom {
					tbl = &tables[ti]
				}
			}
			if tbl == nil {
				out.WriteString(string(rs[i : end+1]))
				i = end
				continue
			}
			ref, err := tbl.resolve(string(rs[i+1:end]), row)
			if err != nil {
				out.WriteString(string(rs[i : end+1]))
				i = end
				continue
			}
			prefix := text[:k]
			out.Reset()
			out.WriteString(prefix + ref)
			i = end
		default:
			out.WriteRune(ch)
		}
	}
	return out.String()
}

// matchStructuredBracket returns the index of the "]" closing the "[" at start,
// honouring nested brackets and "'" escapes, or -1.
func matchStructuredBracket(rs []rune, start int) int {
	depth := 0
	for i := start; i < len(rs); i++ {
		switch rs[i] {
		case '\'':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

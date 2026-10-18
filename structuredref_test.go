package excelize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func structuredRefWorkbook(t *testing.T) *File {
	f := NewFile()
	rows := [][]any{
		{"Name", "1.1\nVersion\tcontrol ", "Other"},
		{"a", "In Progress (50%)", 1},
		{"b", "Acomplished (100%)", 2},
		{"c", "Not started (0%)", 3},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := CoordinatesToCellName(c+1, r+2)
			require.NoError(t, f.SetCellValue("Sheet1", cell, v))
		}
	}
	require.NoError(t, f.AddTable("Sheet1", &Table{Range: "A2:C5", Name: "Table1"}))
	return f
}

// Header names with newlines, tabs and trailing blanks resolve in structured
// references, and line breaks between operands are blanks, not names.
func TestCalcStructuredReferences(t *testing.T) {
	f := structuredRefWorkbook(t)
	col := "Table1[1.1\nVersion\tcontrol ]"
	for formula, want := range map[string]string{
		`COUNTIF(` + col + `,"Acomplished (100%)")`:            "1",
		"(COUNTIF(" + col + ",\"In Progress (50%)\")*0.5\n/2)": "0.25",
		`SUM(Table1[Other])`:                                   "6",
		`SUM(Table1[[Other]])`:                                 "6",
		`SUM(Table1[[#Data],[Other]])`:                         "6",
		`COUNTA(Table1[[#Headers],[Other]])`:                   "1",
		`ROWS(Table1[#All])`:                                   "4",
		`SUM(Table1[[Name]:[Other]])`:                          "6",
		`INDEX(Table1[Other],2)`:                               "2",
		`"Table1[Other]"&"x"`:                                  "Table1[Other]x",
	} {
		require.NoError(t, f.SetCellFormula("Sheet1", "H1", formula))
		got, err := f.CalcCellValue("Sheet1", "H1")
		require.NoError(t, err, formula)
		assert.Equal(t, want, got, formula)
	}
	require.NoError(t, f.SetCellFormula("Sheet1", "D4", "SUM(Table1[@Other],Table1[[#This Row],[Other]])"))
	got, err := f.CalcCellValue("Sheet1", "D4")
	require.NoError(t, err)
	assert.Equal(t, "4", got)
}

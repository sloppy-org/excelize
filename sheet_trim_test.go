package excelize

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTrimRowKeepsOrderAndDropsEmptyRows(t *testing.T) {
	sheetData := &xlsxSheetData{Row: []xlsxRow{
		{R: 1, C: []xlsxC{{R: "A1", V: "1"}}},
		{R: 2},
		{R: 3, C: []xlsxC{{R: "A3"}, {R: "B3", V: "x"}}},
		{R: 4, C: []xlsxC{{R: "A4"}}},
		{R: 5, Ht: float64Ptr(15), CustomHeight: true},
	}}
	rows := trimRow(sheetData)
	assert.Len(t, rows, 3)
	assert.Equal(t, 1, rows[0].R)
	assert.Equal(t, 3, rows[1].R)
	assert.Equal(t, []xlsxC{{R: "B3", V: "x"}}, rows[1].C)
	assert.Equal(t, 5, rows[2].R)
}

// A single styled row at the last worksheet row pads the sheet to 1,048,576
// rows in memory. Saving must drop the empty ones in linear time: the old
// trimRow removed them one by one and never finished, which hung helpy's
// sheet_recalc on a real workbook.
func TestSaveSheetWithStyledLastRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last-row.xlsx")
	done := make(chan error, 1)
	go func() {
		f := NewFile()
		for _, err := range []error{
			f.SetCellValue("Sheet1", "A1", 21),
			f.SetCellFormula("Sheet1", "B1", "A1*2"),
			f.SetRowHeight("Sheet1", TotalRows, 15),
			f.SaveAs(path),
			f.Close(),
		} {
			if err != nil {
				done <- err
				return
			}
		}
		f, err := OpenFile(path)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = f.Close() }()
		if err := f.Recalc(); err != nil {
			done <- err
			return
		}
		done <- f.Save()
	}()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("saving a sheet padded to the last row did not finish in 30s")
	}

	f, err := OpenFile(path)
	assert.NoError(t, err)
	defer func() { _ = f.Close() }()
	value, err := f.GetCellValue("Sheet1", "B1")
	assert.NoError(t, err)
	assert.Equal(t, "42", value)
	height, err := f.GetRowHeight("Sheet1", TotalRows)
	assert.NoError(t, err)
	assert.Equal(t, 15.0, height)
}

package businessflow

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseBundleActionXLSXStreamsRowsAndPreservesCounts(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "actions.xlsx")
	xl := excelize.NewFile()
	sheet := xl.GetSheetName(0)
	if err := xl.SetSheetRow(sheet, "A1", &[]string{"uid"}); err != nil {
		t.Fatal(err)
	}
	for cell, value := range map[string]string{
		"A2": " uid-a ",
		"A3": "uid-a",
		"B4": "ignored",
		"A5": "uid-b",
	} {
		if err := xl.SetCellValue(sheet, cell, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := xl.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := xl.Close(); err != nil {
		t.Fatal(err)
	}

	uids, total, duplicates, invalid, err := parseBundleActionXLSX(path)
	if err != nil {
		t.Fatalf("parseBundleActionXLSX() error = %v", err)
	}
	if got, want := total, int64(4); got != want {
		t.Errorf("total = %d, want %d", got, want)
	}
	if got, want := duplicates, int64(1); got != want {
		t.Errorf("duplicates = %d, want %d", got, want)
	}
	if got, want := invalid, int64(1); got != want {
		t.Errorf("invalid = %d, want %d", got, want)
	}
	if want := []string{"uid-a", "uid-b"}; !reflect.DeepEqual(uids, want) {
		t.Errorf("uids = %#v, want %#v", uids, want)
	}
}

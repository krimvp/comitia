package comitia_test

import (
	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/storetest"
	"testing"
)

func TestSQLiteGrantConformance(t *testing.T) {
	storetest.Grants(t, func(t *testing.T) comitia.Store {
		t.Helper()
		st, err := comitia.OpenSQLite(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		return st
	})
}

package update

import (
	"reflect"
	"testing"
)

func TestNormalizeDatasetsValidatesDeduplicatesAndOrders(t *testing.T) {
	got, err := normalizeDatasets([]string{"logs", "transactions", "logs", "balances"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"transactions", "logs", "balances"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeDatasets = %v, want %v", got, want)
	}
	if _, err := normalizeDatasets([]string{"not-a-dataset"}); err == nil {
		t.Fatal("expected invalid dataset error")
	}
}

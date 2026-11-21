package e2e

import (
	"testing"

	"example.com/app/internal/di"
)

func TestOrdersAPI(t *testing.T) {
	if err := di.InitializeOrderHandler().HandleOrder("e2e"); err != nil {
		t.Fatal(err)
	}
}

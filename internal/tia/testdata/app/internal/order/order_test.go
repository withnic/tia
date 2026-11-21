//go:build integration

package order_test

import (
	"testing"

	"example.com/app/internal/order"
)

func TestOrderCreate(t *testing.T) {
	if err := order.NewOrderService().Create("o"); err != nil {
		t.Fatal(err)
	}
}

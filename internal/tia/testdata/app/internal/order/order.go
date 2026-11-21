// Package order has a Create method of its own: changes to repo.Create must
// not select its tests, since it does not import repo.
package order

type OrderService struct{ n int }

func NewOrderService() *OrderService { return &OrderService{} }

func (s *OrderService) Create(_ string) error {
	s.n++
	return nil
}

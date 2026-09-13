package order

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// Where orders live.
//
// One sidecar store with the atomic-write and fsync discipline the other
// design-time stores follow. An order is durable from the moment it is placed:
// somebody asked for something, and that fact outlives the process that fulfils
// it — which is also why the store lists newest first, since the question anybody
// arrives with is about a recent order.

// Store holds orders.
type Store struct {
	orders *sidecar.Store[Order]
}

// NewStore opens (creating if needed) the directory backing the order store.
func NewStore(dir string) (*Store, error) {
	orders, err := sidecar.NewStore(dir, "orderstore",
		func(o Order) string { return o.ID },
		sidecar.Order(func(a, b Order) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt > b.CreatedAt
			}
			return a.ID < b.ID
		}))
	if err != nil {
		return nil, err
	}
	return &Store{orders: orders}, nil
}

// Save writes an order, replacing any record with the same id.
func (s *Store) Save(o Order) error { return s.orders.Save(o) }

// Get reads one order. A missing record is not an error.
func (s *Store) Get(id string) (Order, bool, error) { return s.orders.Get(id) }

// All lists every order, newest first.
func (s *Store) All() ([]Order, error) { return s.orders.LoadAll() }

// For lists the orders one principal placed or is the recipient of, newest
// first. Both, because an order placed for somebody by their integration manager
// is one they should be able to see.
func (s *Store) For(principal string) ([]Order, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	out := []Order{}
	for _, o := range all {
		if o.Orderer == principal || o.Recipient == principal {
			out = append(out, o)
		}
	}
	return out, nil
}

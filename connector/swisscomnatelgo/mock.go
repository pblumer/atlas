// Package swisscomnatelgo provides a contract-independent, in-memory simulation
// of the publicly advertised NATEL go capabilities. It is NOT a Swisscom API client.
// Real endpoint paths, authentication and payloads require the provider's API contract.
package swisscomnatelgo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Operation string

const (
	Activate         Operation = "activate-subscription"
	PortNumber       Operation = "port-number"
	ConfigureOptions Operation = "configure-options"
	ConfigureRoaming Operation = "configure-roaming"
	OrderSIM         Operation = "order-sim"
	ActivateSIM      Operation = "activate-sim"
	GetPUK           Operation = "get-puk"
	ListInventory    Operation = "list-inventory"
	GetSubscription  Operation = "get-subscription"
	GetOrder         Operation = "get-order"
	Report           Operation = "report"
)

var ErrUnsupportedLive = errors.New("swisscomnatelgo: live API is disabled until official API contract is available")
var ErrNotFound = errors.New("swisscomnatelgo: resource not found")
var ErrConflict = errors.New("swisscomnatelgo: resource already exists")
var ErrInvalid = errors.New("swisscomnatelgo: invalid request")

type Request struct {
	Operation      Operation       `json:"operation"`
	SubscriptionID string          `json:"subscriptionId,omitempty"`
	Number         string          `json:"number,omitempty"`
	SimID          string          `json:"simId,omitempty"`
	SIMType        string          `json:"simType,omitempty"`
	Options        map[string]bool `json:"options,omitempty"`
	Roaming        map[string]bool `json:"roaming,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

type Subscription struct {
	ID      string          `json:"id"`
	Number  string          `json:"number"`
	Status  string          `json:"status"`
	Options map[string]bool `json:"options"`
	Roaming map[string]bool `json:"roaming"`
	SIMIDs  []string        `json:"simIds"`
}

type SIM struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscriptionId"`
	Type           string `json:"type"`
	Status         string `json:"status"`
}

type Result struct {
	Subscription *Subscription  `json:"subscription,omitempty"`
	SIM          *SIM           `json:"sim,omitempty"`
	Inventory    []Subscription `json:"inventory,omitempty"`
	OrderID      string         `json:"orderId,omitempty"`
	OrderStatus  string         `json:"orderStatus,omitempty"`
	// A mock PUK is synthetic and must never be used as a real security credential.
	PUK   string `json:"puk,omitempty"`
	Count int    `json:"count,omitempty"`
}

type Client interface {
	Do(context.Context, Request) (Result, error)
}

type Live struct{}

func (Live) Do(context.Context, Request) (Result, error) {
	return Result{}, ErrUnsupportedLive
}

type Mock struct {
	mu     sync.Mutex
	next   uint64
	subs   map[string]Subscription
	sims   map[string]SIM
	orders map[string]Result
	idem   map[string]Result
}

func NewMock() *Mock {
	return &Mock{
		subs:   map[string]Subscription{},
		sims:   map[string]SIM{},
		orders: map[string]Result{},
		idem:   map[string]Result{},
	}
}

func cloneMap(m map[string]bool) map[string]bool {
	r := map[string]bool{}
	for k, v := range m {
		r[k] = v
	}
	return r
}

func cloneSub(s Subscription) Subscription {
	s.Options = cloneMap(s.Options)
	s.Roaming = cloneMap(s.Roaming)
	s.SIMIDs = append([]string(nil), s.SIMIDs...)
	return s
}

func validNumber(n string) bool {
	if !strings.HasPrefix(n, "+") || len(n) < 9 || len(n) > 16 {
		return false
	}
	for _, c := range n[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (m *Mock) Do(ctx context.Context, r Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	mutating := r.Operation == Activate || r.Operation == PortNumber || r.Operation == ConfigureOptions || r.Operation == ConfigureRoaming || r.Operation == OrderSIM || r.Operation == ActivateSIM
	key := string(r.Operation) + ":" + r.IdempotencyKey
	if mutating && r.IdempotencyKey != "" {
		if v, ok := m.idem[key]; ok {
			return v, nil
		}
	}
	var out Result
	switch r.Operation {
	case Activate:
		if !validNumber(r.Number) {
			return out, fmt.Errorf("%w: E.164 number required", ErrInvalid)
		}
		for _, s := range m.subs {
			if s.Number == r.Number {
				return out, ErrConflict
			}
		}
		m.next++
		id := fmt.Sprintf("mock-sub-%06d", m.next)
		s := Subscription{ID: id, Number: r.Number, Status: "active", Options: map[string]bool{}, Roaming: map[string]bool{}}
		m.subs[id] = s
		copy := cloneSub(s)
		out.Subscription = &copy
	case PortNumber:
		if !validNumber(r.Number) || r.SubscriptionID == "" {
			return out, ErrInvalid
		}
		s, ok := m.subs[r.SubscriptionID]
		if !ok {
			return out, ErrNotFound
		}
		for id, other := range m.subs {
			if id != s.ID && other.Number == r.Number {
				return out, ErrConflict
			}
		}
		s.Number = r.Number
		m.subs[s.ID] = s
		copy := cloneSub(s)
		out.Subscription = &copy
	case ConfigureOptions, ConfigureRoaming:
		s, ok := m.subs[r.SubscriptionID]
		if !ok {
			return out, ErrNotFound
		}
		if r.Operation == ConfigureOptions {
			s.Options = cloneMap(r.Options)
		} else {
			s.Roaming = cloneMap(r.Roaming)
		}
		m.subs[s.ID] = s
		copy := cloneSub(s)
		out.Subscription = &copy
	case OrderSIM:
		s, ok := m.subs[r.SubscriptionID]
		if !ok {
			return out, ErrNotFound
		}
		if r.SIMType != "physical" && r.SIMType != "esim" {
			return out, ErrInvalid
		}
		m.next++
		id := fmt.Sprintf("mock-sim-%06d", m.next)
		sim := SIM{ID: id, SubscriptionID: s.ID, Type: r.SIMType, Status: "ordered"}
		m.sims[id] = sim
		s.SIMIDs = append(s.SIMIDs, id)
		m.subs[s.ID] = s
		out.SIM = &sim
	case ActivateSIM:
		sim, ok := m.sims[r.SimID]
		if !ok {
			return out, ErrNotFound
		}
		sim.Status = "active"
		m.sims[sim.ID] = sim
		out.SIM = &sim
	case GetPUK:
		if _, ok := m.sims[r.SimID]; !ok {
			return out, ErrNotFound
		}
		out.PUK = "00000000"
	case GetSubscription:
		s, ok := m.subs[r.SubscriptionID]
		if !ok {
			return out, ErrNotFound
		}
		copy := cloneSub(s)
		out.Subscription = &copy
	case ListInventory, Report:
		keys := make([]string, 0, len(m.subs))
		for k := range m.subs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Inventory = append(out.Inventory, cloneSub(m.subs[k]))
		}
		out.Count = len(keys)
	case GetOrder:
		v, ok := m.orders[r.SubscriptionID]
		if !ok {
			return out, ErrNotFound
		}
		return v, nil
	default:
		return out, fmt.Errorf("%w: operation %q", ErrInvalid, r.Operation)
	}
	if mutating {
		m.next++
		out.OrderID = fmt.Sprintf("mock-order-%06d", m.next)
		out.OrderStatus = "completed"
		m.orders[out.OrderID] = out
		if r.IdempotencyKey != "" {
			m.idem[key] = out
		}
	}
	return out, nil
}

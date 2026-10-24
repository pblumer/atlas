# Swisscom Smart Catalogs for NATEL go — mock foundation

**Status: experimental, mock-only; NOT an executable Atlas Worker Type yet.**

The public API directory describes these capabilities: activation, portability,
subscription options, roaming configuration, SIM/eSIM orders and activation, PUK
retrieval, inventory and reporting. It does **not** publish the detailed endpoint
paths, HTTP methods, OAuth scopes, request/response schemas, pagination, error
semantics or authoritative set of operations. See:
https://digital.swisscom.com/products/smartcatalogs-natelgo
https://apis.io/apis/swisscom/swisscom-smart-catalogs-natel-go-api/

This branch adds a provider-independent Go client interface and a stateful,
thread-safe in-memory simulator in connector/swisscomnatelgo. It is a foundation,
not a production integration. The Live implementation intentionally fails closed.

## Simulated operations

- activate-subscription
- port-number
- configure-options
- configure-roaming
- order-sim (physical or esim)
- activate-sim
- get-puk (synthetic value 00000000, never a real credential)
- list-inventory
- get-subscription
- get-order (simulation-only lifecycle convention)
- report (simulation-only inventory summary)

All IDs, order status transitions and object shapes are mock conventions, **not**
Swisscom contract representations. The simulator is process-local and all state
disappears on restart. It cannot validate Swisscom business rules. No real API
request is made, and credentials are not required.

## Before wiring it as an Atlas Worker Type

1. Obtain official Swisscom OpenAPI/Swagger (or equivalent), authentication
   guide and a test tenant from enterprise account management.
2. Create a contract inventory: documented operations, field types, required
   privileges, pagination, asynchronous order handling, errors and rate limits.
3. Add an ADR and reserve a stable Atlas job-type index.
4. Add compiler task validation and FEEL binding, worker execution, registration,
   Modeler catalog metadata, configuration, secrets handling, setup docs and handbook.
5. Implement real HTTP client only from verified paths; block calls without the
   actual contract; do not send PUKs to normal logs.
6. TDD: add contract fixtures, mock/live parity, idempotency, network failure,
   retry and cancellation tests, then run full build, race, vet and Windows CI.

## Local unit test

`go test ./connector/swisscomnatelgo/...`

Not tested against Swisscom; not part of worker registration or UI yet.

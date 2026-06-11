# Graph Compiler

The compiler is the bridge between the human-facing world of BPMN (string IDs, nested, declarative) and the machine-facing world of the runtime (flat, integer-indexed slices traversed with cache-friendly lookups). It runs **once per deployment** and produces an immutable `CompiledProcess`.

## Guiding principle: expensive once, cheap a million times

Compilation is rare compared to execution, so it is allowed to be thorough: parse XML, validate, intern strings, resolve indices, pre-compile expressions. The result is read-only and read concurrently by many goroutines without locks.

## Pipeline

```
BPMN XML
  → [1] Parse         XML → raw object tree (strings, pointers)
  → [2] Resolve       string IDs → integer indices; wire flows (two passes)
  → [3] Intern        strings (job types, var names, message names) → index tables
  → [4] Compile expr  FEEL conditions/mappings → prepared AST / bytecode
  → [5] Validate      reachability, gateway coverage, scope consistency
  → [6] Linearize     pour into flat, indexed slices
  → CompiledProcess (immutable)
```

## Target structure

```go
type CompiledProcess struct {
    Key           uint64    // ProcessDefinitionKey
    BpmnProcessId int32     // interned
    Version       int32

    nodes    []CompiledNode  // indexed by ElementId
    flows    []CompiledFlow
    scopes   []ScopeInfo

    // shared, densely-packed topology tables
    outgoingFlows []int32
    incomingFlows []int32

    // type-specific detail tables (grouped by type for cache locality)
    serviceTasks   []ServiceTaskDetail
    timers         []TimerDetail
    gateways       []GatewayDetail
    callActivities []CallActivityDetail

    // compiled expressions, referenced by index
    expressions []CompiledExpression

    strings     []string   // intern table (index → string; debug/export only)
    startEvents []int32     // entry points
}

type CompiledNode struct {
    ElementId int32     // == index in nodes[]  (self-referential)
    Type      BpmnType  // uint8 enum
    Name      int32     // interned, -1 if empty

    IncomingStart int32  // offset into incomingFlows
    IncomingCount int32
    OutgoingStart int32  // offset into outgoingFlows
    OutgoingCount int32

    FlowScope int32      // ElementId of enclosing scope, -1 = process root
    Detail    int32      // index into the matching detail table
    DefaultFlow int32    // for gateways, -1 if none
}

type CompiledFlow struct {
    Id        int32
    Source    int32      // ElementId
    Target    int32      // ElementId
    Condition int32      // index into expressions, -1 = unconditional
}
```

## Design decision 1: struct-of-arrays topology

Giving each node its own `[]int32` of outgoing flows means a slice header plus a separate heap allocation per node — thousands of tiny allocations scattered in memory, causing cache misses on traversal and GC pressure.

Instead, all outgoing-flow indices live in one shared array; nodes index into it:

```go
func (p *CompiledProcess) Outgoing(nodeId int32) []int32 {
    n := &p.nodes[nodeId]
    return p.outgoingFlows[n.OutgoingStart : n.OutgoingStart+n.OutgoingCount]
}
```

The entire topology now lives in two contiguous arrays — ideal for the CPU prefetcher during `takeOutgoingFlows`.

## Design decision 2: detail tables instead of fat nodes

A service task needs job type + retries; a timer needs a timer definition; a gateway needs neither. If `CompiledNode` had fields for all of them, every node would be bloated (you'd load bytes you never read). Instead each node holds a `Detail int32` index into a type-specific table:

```go
type ServiceTaskDetail struct {
    JobType        int32   // interned
    Retries        int32
    InputMappings  Span    // offset+count into a mapping table
    OutputMappings Span
}

type TimerDetail struct {
    Kind       TimerKind   // Date | Duration | Cycle
    Expression int32       // compiled expression index
}
```

`CompiledNode` stays small; details are grouped by type for locality.

## Stage 2: resolve (two passes)

Flows may reference nodes defined later in the XML (forward references), so resolution is two passes:

```go
func (c *compiler) resolve(raw *rawProcess) error {
    // Pass 1: register all nodes → build ID→index map
    for _, el := range raw.elements {
        id := int32(len(c.nodes))
        c.nodeIndex[el.Id] = id
        c.nodes = append(c.nodes, CompiledNode{
            ElementId: id,
            Type:      mapBpmnType(el.Tag),
            Name:      c.interner.intern(el.Name),
            FlowScope: -1,
        })
    }
    // Pass 2: resolve flows — all node IDs known now
    for _, f := range raw.flows {
        src, ok1 := c.nodeIndex[f.SourceRef]
        tgt, ok2 := c.nodeIndex[f.TargetRef]
        if !ok1 || !ok2 {
            return fmt.Errorf("flow %s references unknown element", f.Id)
        }
        flowId := int32(len(c.flows))
        c.flowIndex[f.Id] = flowId
        c.flows = append(c.flows, CompiledFlow{
            Id: flowId, Source: src, Target: tgt,
            Condition: c.compileCondition(f.ConditionExpr),
        })
    }
    return nil
}
```

The `map[string]int32` exists *only during compilation*. The finished `CompiledProcess` has no string lookups on the hot path.

## Stage 3: string interning

Each recurring string (job type `"payment"`, variable name `"orderId"`, message name) becomes an `int32`. Saves memory and turns comparisons into integer comparisons. At runtime, a worker polling for `"payment"` jobs compares `int32 == int32`.

## Stage 4: expression compilation

BPMN is full of expressions: gateway conditions, input/output mappings, timer definitions, correlation keys. The standard language is **FEEL**. Parsing these at runtime would be fatal — it sits on the hot path at every gateway decision. So expressions are compiled to a prepared AST (or bytecode for a small stack VM):

```go
type CompiledExpression struct {
    root   ExprNode   // or: bytecode []Op
    inputs []int32    // which variable indices this expression reads
}

func (e *CompiledExpression) Eval(vars *VariableScope) (Value, error) {
    return e.root.eval(vars)  // hot path: no allocation, no parsing
}
```

The `inputs` hint lets the processor load only the variables an expression needs instead of materializing the whole scope. See [ADR-0008](../adr/0008-feel-expression-strategy.md).

## Stage 5: validation — fail at deploy, never at runtime

The compiler is where structural errors are caught while a human is still watching:

- **Reachability** — every node must be reachable from a start event (else it's dead code).
- **Gateway coverage** — an exclusive gateway with only conditional outgoing flows and no default can deadlock; warn or error.
- **Scope consistency** — boundary events must attach to an element that exists.
- **Expression variables** — referenced variables should exist where statically checkable.

```go
func (c *compiler) validate() []Diagnostic {
    var diags []Diagnostic
    reachable := c.bfsFromStarts()
    for i := range c.nodes {
        if !reachable[i] {
            diags = append(diags, warn("element not reachable", c.nodes[i].ElementId))
        }
    }
    for i := range c.nodes {
        if c.nodes[i].Type == ExclusiveGateway && c.nodes[i].DefaultFlow == -1 {
            diags = append(diags, c.checkGatewayCoverage(int32(i))...)
        }
    }
    return diags
}
```

## Stage 6: linearization and the scope model

BPMN is hierarchical; the runtime model is flat. Hierarchy becomes index references. Each node gets a `FlowScope` (the index of its enclosing scope; `-1` for the process root). Each scope records bookkeeping the processor needs:

```go
type ScopeInfo struct {
    ElementId          int32
    Parent             int32   // enclosing scope, -1 = process
    ChildCount         int32   // direct children (for completion detection)
    BoundaryEvents     Span    // attached boundary events (offset+count)
    HasEventSubprocess bool
}
```

This is what makes subprocess semantics cheap at runtime. When an element instance completes, it decrements its scope's active-children counter. When the counter hits zero and the scope is "completing", the subprocess is done. The entire subprocess lifecycle reduces to *one integer counter per scope instance* — because the compiler resolved the hierarchy into `FlowScope` indices and `ChildCount` ahead of time.

## Versioning

Process instances can run for weeks, so version 1 of a process is still running while version 2 is deployed. The compiler produces one immutable `CompiledProcess` per version, and each element instance carries the `ProcessDefKey` (not just the `BpmnProcessId`). Every instance runs to completion in its birth version — no mixing, no mid-flight migration surprises.

## Runtime payoff

When a service task activates:

```go
func (serviceTaskBehavior) OnActivated(c *ProcessingContext, ei *ElementInstanceValue) {
    cp     := c.proc.process(ei.ProcessDefKey) // immutable CompiledProcess
    node   := &cp.nodes[ei.ElementId]          // O(1) array index
    detail := &cp.serviceTasks[node.Detail]    // O(1) in detail table
    job := &JobValue{
        JobType: detail.JobType,               // already interned (int32)
        Retries: detail.Retries,
    }
    // ...
}
```

Three array indexings, zero allocations, zero string operations, zero locks. That is the compiler's payoff: all the expensive work happened at deploy time, leaving only pointer arithmetic at runtime.

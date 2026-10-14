# Lifecycle strand

`strang.py` merges a catalogue product's provisioning and deprovisioning
processes into **one strand per order position**: the lifecycle form
`per-position` of ADR-0428.

```
<product>.provision → provisioning (copied) → Ausgegeben → Rückgabe angefordert ──┐
                                                                                   ├→ deprovisioning (copied) → end
<product>.deprovision ("Rückgabe ohne laufende Instanz") ─────────────────────────┘
```

- The provisioning process's plain start becomes the message start
  `<product>.provision`.
- The end reached after the step that reports the position `done` becomes the
  wait for the return: a message catch of `<product>.deprovision`, correlated on
  `orderId + "/" + positionId` — the key an order delivers the return with.
  Any other end (a rejection, say) stays an end: nothing was granted, so there
  is nothing to hold.
- The deprovisioning process's plain start becomes a message start of the same
  message, joined in front of its first step, so a right with no running
  instance (an older order, a cancelled instance) can still be returned.
- Every other element — tasks, forms, groups, gateways, reports — is copied
  unchanged, with its id prefixed `p_` or `d_`. Both drawings are kept; the
  deprovisioning one is placed to the right of the handover.

```bash
scripts/lifecycle-strand/strang.py provision.bpmn deprovision.bpmn \
  --product laptop-huelle-14 --process-id proc_laptop_huelle_14_strang \
  --name "Laptop-Hülle: Lebenszyklus" -o strand.bpmn
```

Bind the product with `lifecycleProcess` = the process id, `operations` =
`{"provision": "<product>.provision", "deprovision": "<product>.deprovision"}`
and `lifecycleForm: "per-position"`. Publishing checks the rest.

What it does not do: add a change branch (there is nothing to copy one from),
or merge a provisioning process that never reports `done` — it refuses those.
`strang_test.go` runs it on `testdata/` and holds the result to what the
compiler and the publish check ask of a per-position process.

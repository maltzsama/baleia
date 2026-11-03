---
sidebar_position: 6
---

# Workload management

Workload management decides **when** a run enters its engine. Without it, every run goes straight to the engine. Ten people running at once means ten concurrent queries, and a 50 ms preview competes with a heavy report and a retriever refresh. With it on, each engine gets one queue per class of work. Admins set how much of each class an engine runs at once, and the rest waits its turn.

Baleia never schedules CPU or memory inside an engine; it only admits runs and tags them ([ADR 0014](../adr/0014-workload-management.md)).

## Turn it on

Workload management is off by default. Set `BALEIA_WORKLOAD_ENABLED=true` on the API and the worker ([configuration](../configuration.md)). While it is off, runs start immediately and are never queued.

:::caution Runtime limits change
With it on, each class's **runtime timeout** replaces `BALEIA_STATEMENT_TIMEOUT` and `BALEIA_EXECUTION_TIMEOUT` for the runs of that class. The default for `interactive` is 5 minutes. Review the timeouts in **Settings → Workload** before you turn it on.
:::

## Classes

| Class | For | Gets it by default |
|---|---|---|
| `interactive` | Work someone is waiting for | Runs from the SQL editor and Explore; DDL statements |
| `heavy` | Large, slow work | Result exports and downloads |
| `background` | Work nobody is watching | Agents, retriever refreshes, metadata syncs and Iceberg maintenance |

A run's class is decided once, when it starts, in this order:

1. **Origin:** where the run came from, as in the table above. A DDL statement is never left in `background` by its origin alone.
2. **Rules:** the first enabled rule that matches overrides the origin ([rules](#rules)).
3. **Cost routing:** on a Trino engine with cost routing on, a run whose estimated input is above the threshold becomes `heavy` ([cost routing](#cost-routing-trino)). It only applies when no rule matched.

The class and the reason it was chosen are stored with the execution. Both show in **History** (the **Class** column and filter) and in the execution's details.

## Queues and capacity

Each engine has a queue per class. Admins manage them in **Settings → Workload**, for the engine they pick.

| Setting | Meaning |
|---|---|
| **Max running** | Runs of the class on the engine at once. `0` closes the class: new runs fail at once, and runs already queued wait until their queue timeout |
| **Max queued** | Runs that may wait. Beyond it, a new run fails at once with *the queue is full* |
| **Queue timeout** | How long a run may wait before it fails with *the queue timed out* |
| **Runtime timeout** | How long a run of the class may run once admitted |
| **Engine ceiling** | Optional cap on all classes together on this engine |

An engine with nothing configured uses conservative defaults:

| Class | Federated engine (Trino) | Direct engine | Queue timeout | Runtime timeout |
|---|---|---|---|---|
| `interactive` | 20 running, 200 queued | 4 running, 20 queued | 5 min | 5 min |
| `heavy` | 3 running, 50 queued | 1 running, 10 queued | 30 min | 30 min |
| `background` | 2 running, 100 queued | 1 running, 20 queued | 24 h | 24 h |

The **Live queues** table shows, per class, what is running, what is queued and the oldest wait, refreshed while the page is open. Changes apply at once, without a restart.

### Fairness

Within a class, slots are shared across workspaces by weight (a rule can set a weight; the default is 1). A run's priority rises the longer it waits, so while a class is open its runs are never starved by busier workspaces.

## Rules

Rules let an admin move work between classes. They are evaluated in order, and the first enabled rule that matches decides the class. A rule matches on any combination of:

- the run's **origin** (editor, agent, Explore, retriever, metadata, maintenance)
- the **workspace**, **team**, **role**, **user** or **agent**
- the **engine** or the **saved query**
- the number of **statements** (minimum or maximum)

Every field set must match; an empty field matches anything. **Test a run** shows which class a run with the attributes you enter would get, and why. Use **Move up** and **Move down** to change the order.

## What users see

- **Run panel:** a queued run shows *Queued · position N · ~wait · class*. Users only see the position of their own runs. A queued run can be cancelled like a running one.
- **Errors:** a full queue fails at once. A run that waits past its queue timeout fails with *the queue timed out*. Both explain what to try next.
- **History:** the **Class** filter and column, and the **Queue wait** of each run.
- **Execution details:** the timeline separates the time in Baleia's queue from the engine's own queueing, planning and execution.

## Cost routing (Trino)

On a Trino engine, Baleia can ask the engine for an `EXPLAIN` estimate before classifying a run. A run whose estimated input is above the threshold becomes `heavy`. It is off by default, and only applies when no rule matched, so a matching rule never pays for the estimate.

Turn it on per engine through the API:

```http
PATCH /api/v1/engines/{id}
{ "cost_routing_enabled": true, "cost_threshold_bytes": 10737418240 }
```

## Adaptive admission (Trino)

When the Trino cluster is already saturated, Baleia can hold back everything but `interactive` work until it recovers. The monitoring poller compares each cluster snapshot with three thresholds: queued queries, blocked queries and free memory. While any one is crossed, only `interactive` runs are admitted. The gate lifts on the next snapshot below the thresholds. [Operations](../operations.md#adaptive-admission-trino) lists the settings.

## Agents

An agent's policy caps how many queries it runs at once ([MCP](../mcp.md#policy-and-limits)). With workload management on, a run over that cap waits in its class's queue instead of being refused: `background`, unless a rule or cost routing gives agent runs another class. The hourly quota still refuses.

## Inside the engines

Baleia also passes the class to engines that can act on it. Trino gets the `baleia.class` client tag. ClickHouse, SQL Server, Oracle and PostgreSQL get their native equivalents. On Trino, an admin can go further and let Trino's own resource groups hold the queue. [Operations](../operations.md#workload-tagging-trino) covers both, including the handover to [managed Trino resource groups](../operations.md#managed-trino-resource-groups-a-5).

Queue depth, waits and timeouts are exported as metrics, with the alerts `BaleiaInteractiveQueueSlow` and `BaleiaQueueTimeouts` ([runbooks](../operations.md#baleiainteractivequeueslow)).

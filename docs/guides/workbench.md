---
sidebar_position: 1
---

# SQL workbench

The workbench (**SQL** in the navigation rail) is where you write and run SQL:

- **Left sidebar:** your data sources (with a search over tables and columns) and your saved queries.
- **Middle:** the editor, with the results below it.
- **Right sidebar:** the **Inspector**, with the details of the selected table, and the optional [assistant](../assistant.md).

## Pick a target

Each tab runs against a **target**: an engine binding and, optionally, a schema ([concepts](../concepts.md#bindings)). Pick it in the target selector above the editor. A workspace can set a default target, used when a tab names none.

## Write

- **Autocomplete** suggests what fits at the cursor (statement keywords, tables, columns, clause keywords) from the target catalog's stored metadata.
- **Format SQL** reformats the tab for the engine's dialect.
- **Explain** shows the engine's plan for the statement at the cursor, when the engine supports `EXPLAIN`.
- **Tabs** keep their SQL and target; open as many as you need.

## Run

| Action | Shortcut |
|---|---|
| Run the statement at the cursor, or the selection | ⌘/Ctrl + Enter |
| Run the whole tab as a script | ⌘/Ctrl + Shift + Enter |
| Explain | ⌘/Ctrl + E |
| Format | Shift + Alt + F |
| Save / save as a copy | ⌘/Ctrl + S / ⌘/Ctrl + Shift + S |
| Quick open a saved query | ⌘/Ctrl + P |
| Command palette | ⌘/Ctrl + K |
| New tab / close tab | Alt + N / Alt + W |

A script runs its statements in order, in one engine session, and stops at the first error; the statements after it are marked skipped. Each statement gets its own result tab.

## Results

- **Paging:** results are stored, not held in the browser, so large results scroll page by page.
- **Export:** a result exports as CSV, TSV, JSON Lines or Arrow.
- **Limits:** a statement's stored result is capped by `BALEIA_RESULT_MAX_ROWS` and `BALEIA_RESULT_MAX_BYTES` ([configuration](../configuration.md)).

## Save and share

A saved query lives in a [space](../concepts.md#spaces-and-content) and keeps its target. Every save adds a version: open the history to compare versions or restore one. Deleted queries go to the trash (**Content → Trash**) and can be restored.

When someone else saved the same query since you opened it, Baleia shows the conflict instead of overwriting their change.

## History

**SQL → History** lists every execution in the workspace. You can search it by SQL and filter it by status and time. From there you can open an execution's statements, **Open in editor** to run it again, or cancel one that is still running: the statement running now is stopped on the engine, and statements that already finished keep their results.

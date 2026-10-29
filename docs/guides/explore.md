---
sidebar_position: 3
---

# Explore, models and metrics

Baleia has a semantic layer: describe a table once as a **model**, and anyone can ask questions of it in **Explore** by clicking fields instead of writing SQL. Baleia compiles each question into SQL for the engine it runs on.

## Models

A model (**Explore → Models**) describes one table, or one SQL query in the engine's dialect, with:

- **Dimensions:** the columns you group and filter by. They are generated from the table's columns and can be renamed, or be SQL expressions. Time dimensions offer time grains (day, month, …).
- **Metrics:** aggregations such as a count, a sum or an average. A custom aggregate expression is possible, but it is not portable across engines; prefer an aggregation when one fits. Division is rendered safely: a zero denominator yields NULL.
- **Joins:** to other models, so a question can use fields from several of them.
- **A primary key:** the columns that identify a row.

The editor previews the SQL of a sample question once the model is saved. Models are [content](../concepts.md#spaces-and-content): they live in a space, keep a version history and can be restored from the trash. Deleting a model breaks the Explore links and saved explorations that use it.

## Explore

In **Explore**:

1. Pick a model on the left.
2. Click dimensions and metrics to build the question, and reorder them.
3. Add filters: dimension filters apply before aggregation (`WHERE`), metric filters after it (`HAVING`).
4. Run it, or open the SQL tab to read the compiled query, or **Open SQL in editor** to continue in the workbench.

The question is kept in the URL (`/explore?q=…`), so the link reproduces it for anyone who can see the model. **Save as saved query** stores it in a space.

## Metrics

**Content → Metrics** lists metrics: named aggregations over one table, rendered for whichever engine you run on. These are the first version of metrics; models supersede them. The model editor can migrate them: each table they use becomes a model, each metric keeps its slug, and nothing is deleted.

Agents reach the same layer through the [MCP server](../mcp.md) (`list_models`, `describe_model`, `query_semantic`, `list_metrics`, `render_metric`).

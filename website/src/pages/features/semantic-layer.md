---
title: "Semantic layer for Trino: models, metrics and Explore"
description: "Describe a table once as a model and let anyone explore its dimensions and metrics without SQL: Baleia compiles each question for the engine it runs on."
---

# Semantic layer for Trino: models, metrics and Explore

Baleia has a semantic layer. You describe a table once as a **model**, and anyone can ask questions of it in **Explore** by clicking fields instead of writing SQL. Baleia compiles each question into SQL for the engine it runs on.

## Models

A model describes one table, or one SQL query in the engine's dialect, with:

- **Dimensions**, the columns you group and filter by. They are generated from the table's columns and can be renamed or be SQL expressions. Time dimensions offer time grains such as day and month.
- **Metrics**, aggregations such as a count, a sum or an average. Division is rendered safely: a zero denominator yields NULL.
- **Joins** to other models, so a question can use fields from several of them.
- **A primary key**, the columns that identify a row.

The editor previews the SQL of a sample question once the model is saved. Models are shared content: they live in a team space, keep a version history and can be restored from the trash.

## Explore

Pick a model, click dimensions and metrics to build the question, and add filters: dimension filters apply before aggregation (`WHERE`) and metric filters after it (`HAVING`). Run it, read the compiled SQL, or open it in the workbench to keep going. The question is stored in the URL, so the link reproduces it for anyone who can see the model, and you can save it as a saved query.

## Agents use the same layer

The [MCP server](/features/mcp) exposes the models and metrics to your agents (`list_models`, `describe_model`, `query_semantic`, `list_metrics` and `render_metric`), so an agent asks governed questions instead of guessing SQL.

## Try it

- [Read the Explore, models and metrics guide](/docs/guides/explore)
- [Understand spaces and saved content](/docs/guides/content)
- [Open the live demo](https://demo.baleia.galileostd.io)

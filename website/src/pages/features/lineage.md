---
title: "Table lineage from the SQL your team already runs"
description: "See which saved queries, metrics and models read a table, and which tables a query or model depends on: lineage built from the statements themselves."
---

# Table lineage from the SQL your team already runs

Baleia shows lineage in two directions from a table's detail page, built into the data explorer.

## Used by and depends on

- **Used by.** The saved queries, metrics and semantic models that read the table. Before you change a table, you can see what reads it.
- **Depends on.** For a saved query or a model, the tables it reads. This shows in the workbench sidebar and in the model editor.

A table in either list opens its detail: in the workbench inspector it appears below the list, and elsewhere, such as the model editor, it opens in the data explorer.

## Built from the statements

Lineage comes from the statements themselves, so it stays current as content changes, with no manual mapping to maintain. When a metadata sync finds that a model or metric points at a table or column that no longer exists, the attention bell reports it so someone can fix it.

## Part of the data explorer

Lineage sits next to the table's columns, documentation, tags, owner and certified flag, and the popular-tables view built from your query history.

## Try it

- [Read about lineage in the data explorer guide](/docs/guides/data)
- [Read about saved content and spaces](/docs/guides/content)
- [Open the live demo](https://demo.baleia.galileostd.io)

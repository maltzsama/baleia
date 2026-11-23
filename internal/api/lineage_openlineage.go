package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/openlineage"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// lineageRunEvent maps an authorized content item and its resolvable physical
// dependencies to a consumer-independent OpenLineage event.
func (h *CatalogHandlers) lineageRunEvent(ctx context.Context, ws db.UUID, namespace, runID string, item lineageItem, catalogs map[string]string) (openlineage.RunEvent, bool, error) {
	if item.node.Detail == nil || strings.TrimSpace(*item.node.Detail) == "" {
		return openlineage.RunEvent{}, false, nil
	}
	slug := strings.TrimSpace(*item.node.Detail)
	inputs := make([]openlineage.Dataset, 0, len(item.refs))
	seen := make(map[string]bool)
	for _, ref := range item.refs {
		row, err := h.lineage.GetMetadataTableByRef(ctx, db.GetMetadataTableByRefParams{CatalogID: ref.catalogID, Namespace: ref.namespace, Name: ref.name})
		if errors.Is(err, db.ErrNoRows) {
			continue
		}
		if err != nil {
			return openlineage.RunEvent{}, false, err
		}
		table, missing, err := h.grantedTable(ctx, ws, row.ID.Bytes)
		if err != nil {
			return openlineage.RunEvent{}, false, err
		}
		if missing || catalogs[idString(table.CatalogID)] == "" {
			continue
		}
		parts := append([]string{catalogs[idString(table.CatalogID)]}, table.Namespace...)
		parts = append(parts, table.Name)
		name := strings.Join(parts, ".")
		if !seen[name] {
			inputs = append(inputs, openlineage.Dataset{Namespace: namespace, Name: name})
			seen[name] = true
		}
	}
	if len(inputs) == 0 {
		return openlineage.RunEvent{}, false, nil
	}
	// Place the logical output under the first resolved input's database and
	// schema, giving consumers a stable, qualified dataset identity.
	inputName := inputs[0].Name
	lastDot := strings.LastIndexByte(inputName, '.')
	if lastDot < 0 {
		return openlineage.RunEvent{}, false, nil
	}
	outputName := inputName[:lastDot+1] + "baleia_" + strings.ReplaceAll(repo.UUIDString(ws), "-", "") + "_" + string(item.node.Kind) + "_" + slug
	return openlineage.RunEvent{
		EventType: "COMPLETE", EventTime: time.Now().UTC(), Run: openlineage.Run{RunID: runID},
		Job:    openlineage.Job{Namespace: namespace, Name: "workspace/" + repo.UUIDString(ws) + "/" + string(item.node.Kind) + "/" + slug},
		Inputs: inputs, Outputs: []openlineage.Dataset{{Namespace: namespace, Name: outputName}},
		Producer: "https://github.com/maltzsama/baleia", SchemaURL: "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent",
	}, true, nil
}

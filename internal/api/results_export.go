package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/ipc"
)

// exportFormat is the requested serialization of a stored Arrow result (#26).
type exportFormat string

const (
	exportArrow exportFormat = "arrow"
	exportCSV   exportFormat = "csv"
	exportTSV   exportFormat = "tsv"
	exportJSONL exportFormat = "jsonl"
)

func parseExportFormat(value string) (exportFormat, bool) {
	switch exportFormat(value) {
	case "", exportArrow:
		return exportArrow, true
	case exportCSV, exportTSV, exportJSONL:
		return exportFormat(value), true
	default:
		return "", false
	}
}

func (f exportFormat) contentType() string {
	switch f {
	case exportCSV:
		return "text/csv; charset=utf-8"
	case exportTSV:
		return "text/tab-separated-values; charset=utf-8"
	case exportJSONL:
		return "application/x-ndjson; charset=utf-8"
	default:
		return "application/vnd.apache.arrow.stream"
	}
}

func (f exportFormat) extension() string {
	switch f {
	case exportCSV:
		return "csv"
	case exportTSV:
		return "tsv"
	case exportJSONL:
		return "jsonl"
	default:
		return "arrow"
	}
}

// convertResult writes the stored Arrow IPC object to w in the requested
// format, one record batch at a time: memory stays bounded by the batch size.
func convertResult(w io.Writer, r io.Reader, format exportFormat) error {
	if format == exportArrow {
		_, err := io.Copy(w, r)
		return err
	}

	reader, err := ipc.NewReader(r)
	if err != nil {
		return fmt.Errorf("read arrow result: %w", err)
	}
	defer reader.Release()

	switch format {
	case exportCSV:
		return writeDelimited(w, reader, ',')
	case exportTSV:
		return writeDelimited(w, reader, '\t')
	case exportJSONL:
		return writeJSONL(w, reader)
	default:
		return fmt.Errorf("unsupported export format %q", format)
	}
}

// writeDelimited writes RFC 4180 CSV (or the same escaping with tabs). A header
// row names the columns; nested values are JSON strings.
func writeDelimited(w io.Writer, reader *ipc.Reader, comma rune) error {
	out := csv.NewWriter(w)
	out.Comma = comma
	fields := reader.Schema().Fields()
	header := make([]string, len(fields))
	for i, field := range fields {
		header[i] = field.Name
	}
	if err := out.Write(header); err != nil {
		return err
	}

	for reader.Next() {
		record := reader.RecordBatch()
		columns := record.Columns()
		row := make([]string, len(columns))
		for r := 0; r < int(record.NumRows()); r++ {
			for c, column := range columns {
				row[c] = cellString(column, r)
			}
			if err := out.Write(row); err != nil {
				return err
			}
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		return err
	}
	return reader.Err()
}

// writeJSONL writes one JSON object per row, preserving native JSON for nested
// values and null for nulls.
func writeJSONL(w io.Writer, reader *ipc.Reader) error {
	encoder := json.NewEncoder(w)
	fields := reader.Schema().Fields()
	for reader.Next() {
		record := reader.RecordBatch()
		columns := record.Columns()
		for r := 0; r < int(record.NumRows()); r++ {
			row := make(map[string]any, len(columns))
			for c, column := range columns {
				if column.IsNull(r) {
					row[fields[c].Name] = nil
					continue
				}
				row[fields[c].Name] = column.GetOneForMarshal(r)
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
	}
	return reader.Err()
}

func cellString(column arrow.Array, row int) string {
	if column.IsNull(row) {
		return ""
	}
	switch column.DataType().ID() {
	case arrow.LIST, arrow.STRUCT, arrow.MAP, arrow.FIXED_SIZE_LIST:
		raw, err := json.Marshal(column.GetOneForMarshal(row))
		if err != nil {
			return column.ValueStr(row)
		}
		return string(raw)
	default:
		return column.ValueStr(row)
	}
}

// storedResultSet is one entry of query_statements.result_sets.
type storedResultSet struct {
	ResultIndex int    `json:"result_index"`
	ResultKey   string `json:"result_key"`
	Rows        int64  `json:"rows"`
	Bytes       int64  `json:"bytes"`
	Truncated   bool   `json:"truncated"`
}

// resultKeyFor picks the stored object of a result set, falling back to the
// statement's single key for index 0.
func resultKeyFor(resultSets []byte, fallback *string, index int) string {
	var sets []storedResultSet
	if len(resultSets) > 0 && json.Unmarshal(resultSets, &sets) == nil {
		for _, set := range sets {
			if set.ResultIndex == index {
				return set.ResultKey
			}
		}
	}
	if index == 0 && fallback != nil {
		return *fallback
	}
	return ""
}

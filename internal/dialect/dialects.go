package dialect

import "fmt"

// The v1 dialects. Each block is the whole difference of one engine; shared
// behaviour lives in spec.

var ansiInformationSchema = MetadataQueries{
	Schemas: `SELECT schema_name FROM information_schema.schemata WHERE schema_name <> 'information_schema' ORDER BY schema_name`,
	Tables: `SELECT table_name, CASE WHEN table_type = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END
FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`,
	Columns: `SELECT column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`,
}

func explainPrefix(prefix string) func(string) (string, bool) {
	return func(sql string) (string, bool) { return prefix + sql, true }
}

func trueFalse(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func oneZero(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func prefixed(prefix string) func(string) string {
	return func(s string) string { return prefix + "'" + s + "'" }
}

func wrapped(open, closeBy string) func(string) string {
	return func(s string) string { return open + "'" + s + "'" + closeBy }
}

func xHex(h string) string { return "X'" + h + "'" }

var trinoDialect = register(&spec{
	name:      Trino,
	identOpen: `"`, identClose: `"`,
	lex:         lexRules{identQuotes: `"`},
	catalogs:    true,
	schemaDepth: 1,
	placeholder: question, // EXECUTE ... USING
	explain:     explainPrefix("EXPLAIN "),
	types: map[LogicalType]string{
		TypeBoolean: "BOOLEAN", TypeInt64: "BIGINT", TypeFloat64: "DOUBLE", TypeString: "VARCHAR",
		TypeDate: "DATE", TypeTimestamp: "TIMESTAMP(6)", TypeTimestampTZ: "TIMESTAMP(6) WITH TIME ZONE",
		TypeBinary: "VARBINARY", TypeJSON: "JSON",
	},
	decimalTyp:  func(p, s int) string { return fmt.Sprintf("DECIMAL(%d,%d)", p, s) },
	boolLit:     trueFalse,
	dateLit:     prefixed("DATE "),
	tsLit:       prefixed("TIMESTAMP "),
	tsTZLit:     func(s string) string { return "TIMESTAMP '" + s + " UTC'" },
	binaryLit:   xHex,
	jsonLit:     func(q string) string { return "JSON " + q },
	timeTrunc:   dateTrunc,
	approxCount: func(e string) string { return "approx_distinct(" + e + ")" },
	floatCast:   "DOUBLE",
	metadata:    ansiInformationSchema,
	formatter:   "trino", codemirror: "StandardSQL",
})

var postgresDialect = register(&spec{
	name:      PostgreSQL,
	identOpen: `"`, identClose: `"`,
	maxIdent: 63, identBytes: true,
	lex:         lexRules{identQuotes: `"`, dollarQuotes: true, escapeStrings: true, nestedComments: true},
	schemaDepth: 1,
	placeholder: numbered("$"),
	explain:     explainPrefix("EXPLAIN "),
	types: map[LogicalType]string{
		TypeBoolean: "BOOLEAN", TypeInt64: "BIGINT", TypeFloat64: "DOUBLE PRECISION", TypeString: "TEXT",
		TypeDate: "DATE", TypeTimestamp: "TIMESTAMP(6)", TypeTimestampTZ: "TIMESTAMPTZ(6)",
		TypeBinary: "BYTEA", TypeJSON: "JSONB",
	},
	decimalTyp: func(p, s int) string { return fmt.Sprintf("NUMERIC(%d,%d)", p, s) },
	boolLit:    trueFalse,
	dateLit:    prefixed("DATE "),
	tsLit:      prefixed("TIMESTAMP "),
	tsTZLit:    func(s string) string { return "TIMESTAMPTZ '" + s + "+00'" },
	binaryLit:  func(h string) string { return "decode('" + h + "', 'hex')" },
	jsonLit:    func(q string) string { return "CAST(" + q + " AS JSONB)" },
	timeTrunc:  dateTrunc,
	floatCast:  "DOUBLE PRECISION",
	metadata: MetadataQueries{
		Schemas: `SELECT schema_name FROM information_schema.schemata
WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'pg_toast') AND schema_name NOT LIKE 'pg\_temp\_%' AND schema_name NOT LIKE 'pg\_toast\_temp\_%'
ORDER BY schema_name`,
		Tables: `SELECT table_name, CASE WHEN table_type = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END
FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name`,
		Columns: `SELECT column_name, data_type, is_nullable, ordinal_position
FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`,
	},
	formatter: "postgresql", codemirror: "PostgreSQL",
})

// mysqlTimeTrunc rebuilds the truncated value: MySQL has no date_trunc.
// Results are DATETIME; weeks start on Monday.
func mysqlTimeTrunc(e string, g TimeGrain) (string, error) {
	format := map[TimeGrain]string{
		GrainSecond: "%Y-%m-%d %H:%i:%s", GrainMinute: "%Y-%m-%d %H:%i:00", GrainHour: "%Y-%m-%d %H:00:00",
		GrainMonth: "%Y-%m-01 00:00:00", GrainYear: "%Y-01-01 00:00:00",
	}
	switch g {
	case GrainDay:
		return "CAST(DATE(" + e + ") AS DATETIME)", nil
	case GrainWeek:
		return "CAST(DATE_SUB(DATE(" + e + "), INTERVAL WEEKDAY(" + e + ") DAY) AS DATETIME)", nil
	case GrainQuarter:
		return "CAST(MAKEDATE(YEAR(" + e + "), 1) + INTERVAL (QUARTER(" + e + ") - 1) QUARTER AS DATETIME)", nil
	}
	return "CAST(DATE_FORMAT(" + e + ", '" + format[g] + "') AS DATETIME)", nil
}

func mysqlFamily(name, formatter, codemirror string, json bool) *spec {
	types := map[LogicalType]string{
		TypeInt64: "SIGNED", TypeFloat64: "DOUBLE", TypeString: "CHAR",
		TypeDate: "DATE", TypeTimestamp: "DATETIME(6)", TypeBinary: "BINARY",
	}
	var jsonLit func(string) string
	if json {
		types[TypeJSON] = "JSON"
		jsonLit = func(q string) string { return "CAST(" + q + " AS JSON)" }
	}
	return &spec{
		name:      name,
		identOpen: "`", identClose: "`",
		maxIdent:         64,
		backslashStrings: true,
		lex: lexRules{
			identQuotes: "`", doubleStrings: true, backslashStrings: true,
			hashComments: true, delimiterError: true,
		},
		schemaDepth: 1, // a database is a schema; the catalog is always "def"
		placeholder: question,
		explain:     explainPrefix("EXPLAIN "),
		types:       types,
		decimalTyp:  func(p, s int) string { return fmt.Sprintf("DECIMAL(%d,%d)", p, s) },
		boolLit:     trueFalse,
		dateLit:     prefixed("DATE "),
		tsLit:       prefixed("TIMESTAMP "),
		binaryLit:   xHex,
		jsonLit:     jsonLit,
		timeTrunc:   mysqlTimeTrunc,
		metadata: MetadataQueries{
			Schemas: `SELECT schema_name FROM information_schema.schemata
WHERE schema_name NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys') ORDER BY schema_name`,
			Tables: `SELECT table_name, CASE WHEN table_type = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END
FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`,
			Columns: `SELECT column_name, column_type, is_nullable, ordinal_position
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`,
		},
		formatter: formatter, codemirror: codemirror,
	}
}

var (
	mysqlDialect = register(mysqlFamily(MySQL, "mysql", "MySQL", true))
	// MariaDB has no CAST(... AS JSON); JSON is a LONGTEXT alias there.
	mariadbDialect = register(mysqlFamily(MariaDB, "mariadb", "MariaSQL", false))
)

var tsqlDialect = register(&spec{
	name:      TSQL,
	identOpen: "[", identClose: "]",
	maxIdent:        128,
	nationalStrings: true,
	lex:             lexRules{identQuotes: `"`, brackets: true, goSeparator: true, nestedComments: true},
	catalogs:        true,
	schemaDepth:     1,
	placeholder:     numbered("@p"),
	preview:         func(obj string, n int) string { return fmt.Sprintf("SELECT TOP (%d) * FROM %s", n, obj) },
	limit: func(sql string, n int) string {
		return fmt.Sprintf("SELECT TOP (%d) * FROM (%s) AS baleia_limit", n, sql)
	},
	types: map[LogicalType]string{
		TypeBoolean: "BIT", TypeInt64: "BIGINT", TypeFloat64: "FLOAT", TypeString: "NVARCHAR(MAX)",
		TypeDate: "DATE", TypeTimestamp: "DATETIME2(6)", TypeTimestampTZ: "DATETIMEOFFSET(6)",
		TypeBinary: "VARBINARY(MAX)", TypeJSON: "NVARCHAR(MAX)",
	},
	decimalTyp: func(p, s int) string { return fmt.Sprintf("DECIMAL(%d,%d)", p, s) },
	boolLit:    func(b bool) string { return "CAST(" + oneZero(b) + " AS BIT)" },
	dateLit:    wrapped("CAST(", " AS DATE)"),
	tsLit:      wrapped("CAST(", " AS DATETIME2(6))"),
	tsTZLit:    func(s string) string { return "CAST('" + s + " +00:00' AS DATETIMEOFFSET(6))" },
	binaryLit:  func(h string) string { return "0x" + h },
	timeTrunc: func(e string, g TimeGrain) (string, error) {
		unit := string(g)
		if g == GrainWeek {
			unit = "iso_week" // week depends on DATEFIRST; iso_week starts on Monday
		}
		return "DATETRUNC(" + unit + ", " + e + ")", nil // SQL Server 2022+
	},
	approxCount: func(e string) string { return "APPROX_COUNT_DISTINCT(" + e + ")" },
	floatCast:   "FLOAT",
	metadata: MetadataQueries{
		Schemas: `SELECT s.name FROM sys.schemas s
WHERE s.name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest') AND s.principal_id < 16384 AND s.schema_id < 16384
ORDER BY s.name`,
		Tables: `SELECT TABLE_NAME, CASE WHEN TABLE_TYPE = 'VIEW' THEN 'VIEW' ELSE 'TABLE' END
FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = @p1 ORDER BY TABLE_NAME`,
		Columns: `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, ORDINAL_POSITION
FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 ORDER BY ORDINAL_POSITION`,
	},
	formatter: "transactsql", codemirror: "MSSQL",
})

var oracleDialect = register(&spec{
	name:      Oracle,
	identOpen: `"`, identClose: `"`,
	maxIdent: 128, identBytes: true,
	identForbidden: `"`, // ORA-25716: no escape exists
	lex:            lexRules{identQuotes: `"`, qQuotes: true, plsqlBlocks: true},
	schemaDepth:    1, // the catalog is the service the connection opened
	placeholder:    numbered(":"),
	preview: func(obj string, n int) string {
		return fmt.Sprintf("SELECT * FROM %s FETCH FIRST %d ROWS ONLY", obj, n)
	},
	limit: func(sql string, n int) string {
		return fmt.Sprintf("SELECT * FROM (%s) FETCH FIRST %d ROWS ONLY", sql, n)
	},
	types: map[LogicalType]string{
		TypeBoolean: "BOOLEAN", // 23ai
		TypeInt64:   "NUMBER(19)", TypeFloat64: "BINARY_DOUBLE", TypeString: "VARCHAR2(4000)",
		TypeDate: "DATE", TypeTimestamp: "TIMESTAMP(6)", TypeTimestampTZ: "TIMESTAMP(6) WITH TIME ZONE",
		TypeBinary: "RAW(2000)", TypeJSON: "JSON",
	},
	decimalTyp: func(p, s int) string { return fmt.Sprintf("NUMBER(%d,%d)", p, s) },
	boolLit:    trueFalse, // 23ai
	dateLit:    prefixed("DATE "),
	tsLit:      prefixed("TIMESTAMP "),
	tsTZLit:    func(s string) string { return "TIMESTAMP '" + s + " +00:00'" },
	binaryLit:  func(h string) string { return "HEXTORAW('" + h + "')" },
	jsonLit:    func(q string) string { return "JSON(" + q + ")" },
	timeTrunc: func(e string, g TimeGrain) (string, error) {
		if g == GrainSecond {
			return "CAST(" + e + " AS DATE)", nil // drops fractional seconds
		}
		f := map[TimeGrain]string{GrainMinute: "MI", GrainHour: "HH24", GrainDay: "DD", GrainWeek: "IW", GrainMonth: "MM", GrainQuarter: "Q", GrainYear: "YYYY"}[g]
		return "TRUNC(" + e + ", '" + f + "')", nil
	},
	approxCount: func(e string) string { return "APPROX_COUNT_DISTINCT(" + e + ")" },
	metadata: MetadataQueries{
		Schemas: `SELECT username FROM all_users WHERE oracle_maintained = 'N' ORDER BY username`,
		Tables: `SELECT object_name, object_type FROM all_objects
WHERE owner = :1 AND object_type IN ('TABLE', 'VIEW') ORDER BY object_name`,
		Columns: `SELECT column_name, data_type, CASE nullable WHEN 'Y' THEN 'YES' ELSE 'NO' END, column_id
FROM all_tab_columns WHERE owner = :1 AND table_name = :2 ORDER BY column_id`,
	},
	formatter: "plsql", codemirror: "PLSQL",
})

// sqliteTimeTrunc returns 'YYYY-MM-DD HH:MM:SS' text, SQLite's datetime form.
func sqliteTimeTrunc(e string, g TimeGrain) (string, error) {
	switch g {
	case GrainSecond:
		return "strftime('%Y-%m-%d %H:%M:%S', " + e + ")", nil
	case GrainMinute:
		return "strftime('%Y-%m-%d %H:%M:00', " + e + ")", nil
	case GrainHour:
		return "strftime('%Y-%m-%d %H:00:00', " + e + ")", nil
	case GrainDay:
		return "datetime(" + e + ", 'start of day')", nil
	case GrainWeek:
		return "datetime(" + e + ", 'weekday 0', '-6 days', 'start of day')", nil
	case GrainMonth:
		return "datetime(" + e + ", 'start of month')", nil
	case GrainQuarter:
		return "datetime(" + e + ", 'start of month', '-' || ((CAST(strftime('%m', " + e + ") AS INTEGER) - 1) % 3) || ' months')", nil
	}
	return "datetime(" + e + ", 'start of year')", nil
}

var sqliteDialect = register(&spec{
	name:      SQLite,
	identOpen: `"`, identClose: `"`,
	lex:         lexRules{identQuotes: "\"`", brackets: true},
	schemaDepth: 0,
	placeholder: numbered("?"), // ?NNN: parameters may repeat out of order
	explain:     explainPrefix("EXPLAIN QUERY PLAN "),
	types: map[LogicalType]string{
		TypeBoolean: "INTEGER", TypeInt64: "INTEGER", TypeFloat64: "REAL", TypeString: "TEXT",
		TypeDate: "date", TypeTimestamp: "datetime", TypeBinary: "BLOB", TypeJSON: "json",
	},
	decimalTyp: func(int, int) string { return "NUMERIC" }, // SQLite keeps no precision
	castFn: func(e string, t LogicalType, target string) string {
		switch t {
		case TypeDate, TypeTimestamp, TypeJSON:
			return target + "(" + e + ")" // date(), datetime(), json() normalize text
		}
		return "CAST(" + e + " AS " + target + ")"
	},
	boolLit:   oneZero,
	dateLit:   func(s string) string { return "'" + s + "'" },
	tsLit:     func(s string) string { return "'" + s + "'" },
	binaryLit: xHex,
	jsonLit:   func(q string) string { return "json(" + q + ")" },
	timeTrunc: sqliteTimeTrunc,
	floatCast: "REAL",
	metadata: MetadataQueries{
		Schemas: `SELECT name FROM pragma_database_list WHERE name <> 'temp' ORDER BY seq`,
		Tables: `SELECT name, upper(type) FROM pragma_table_list
WHERE schema = ?1 AND type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`,
		Columns: `SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END, cid + 1
FROM pragma_table_info(?2, ?1) ORDER BY cid`,
	},
	formatter: "sqlite", codemirror: "SQLite",
})

var clickhouseDialect = register(&spec{
	name:      ClickHouse,
	identOpen: "`", identClose: "`",
	backslashStrings: true,
	lex:              lexRules{identQuotes: "`\"", backslashStrings: true},
	schemaDepth:      1, // databases; one server is one catalog
	placeholder:      func(n int) string { return fmt.Sprintf("{p%d:String}", n) },
	explain:          explainPrefix("EXPLAIN "),
	types: map[LogicalType]string{
		TypeBoolean: "Bool", TypeInt64: "Int64", TypeFloat64: "Float64", TypeString: "String",
		TypeDate: "Date32", TypeTimestamp: "DateTime64(6)", TypeTimestampTZ: "DateTime64(6, 'UTC')",
		TypeBinary: "String",
	},
	decimalTyp:  func(p, s int) string { return fmt.Sprintf("Decimal(%d, %d)", p, s) },
	decimalLit:  func(digits, target string) string { return "CAST('" + digits + "' AS " + target + ")" }, // a bare 1.5 is Float64
	boolLit:     trueFalse,
	dateLit:     wrapped("toDate32(", ")"),
	tsLit:       func(s string) string { return "toDateTime64('" + s + "', 6)" },
	tsTZLit:     func(s string) string { return "toDateTime64('" + s + "', 6, 'UTC')" },
	binaryLit:   func(h string) string { return "unhex('" + h + "')" },
	timeTrunc:   dateTrunc,
	approxCount: func(e string) string { return "uniq(" + e + ")" },
	metadata: MetadataQueries{
		Schemas: `SELECT name FROM system.databases WHERE name NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema') ORDER BY name`,
		Tables: `SELECT name, if(engine IN ('View', 'MaterializedView'), 'VIEW', 'TABLE') FROM system.tables
WHERE database = {p1:String} AND NOT is_temporary ORDER BY name`,
		Columns: `SELECT name, type, if(startsWith(type, 'Nullable('), 'YES', 'NO'), position FROM system.columns
WHERE database = {p1:String} AND table = {p2:String} ORDER BY position`,
	},
	formatter: "sql", codemirror: "MySQL", // backtick identifiers
})

// flightSQLDialect is the ANSI subset: a Flight SQL server can be anything.
// The adapter may switch to a specific dialect when GetSqlInfo names the
// server.
var flightSQLDialect = register(&spec{
	name:      FlightSQL,
	identOpen: `"`, identClose: `"`,
	lex:         lexRules{identQuotes: `"`},
	catalogs:    true,
	schemaDepth: -1,
	placeholder: question,
	types: map[LogicalType]string{
		TypeBoolean: "BOOLEAN", TypeInt64: "BIGINT", TypeFloat64: "DOUBLE PRECISION", TypeString: "VARCHAR",
		TypeDate: "DATE", TypeTimestamp: "TIMESTAMP", TypeTimestampTZ: "TIMESTAMP WITH TIME ZONE",
		TypeBinary: "VARBINARY",
	},
	decimalTyp: func(p, s int) string { return fmt.Sprintf("DECIMAL(%d,%d)", p, s) },
	boolLit:    trueFalse,
	dateLit:    prefixed("DATE "),
	tsLit:      prefixed("TIMESTAMP "),
	tsTZLit:    func(s string) string { return "TIMESTAMP '" + s + "+00:00'" },
	binaryLit:  xHex,
	timeTrunc:  dateTrunc, // not ANSI, but what Flight SQL servers (DuckDB, Dremio, DataFusion) share
	floatCast:  "DOUBLE PRECISION",
	metadata:   ansiInformationSchema,
	formatter:  "sql", codemirror: "StandardSQL",
})

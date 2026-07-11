package sqlsplitter_test

import (
	"testing"

	"github.com/galileostudio/baleia/internal/sqlsplitter"
)

func TestSingleStatement(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1")
	if len(result) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(result))
	}
	if result[0] != "SELECT 1" {
		t.Errorf("got %q", result[0])
	}
}

func TestMultipleStatements(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1; SELECT 2; SELECT 3")
	if len(result) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(result))
	}
}

func TestEmptyStatements(t *testing.T) {
	result := sqlsplitter.Split(";;SELECT 1;;")
	if len(result) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(result))
	}
}

func TestStringLiteral(t *testing.T) {
	result := sqlsplitter.Split("SELECT 'hello; world'")
	if len(result) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(result))
	}
	if result[0] != "SELECT 'hello; world'" {
		t.Errorf("got %q", result[0])
	}
}

func TestEscapedQuote(t *testing.T) {
	result := sqlsplitter.Split("SELECT 'it''s fine'; SELECT 2")
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(result))
	}
}

func TestDoubleQuotedIdentifier(t *testing.T) {
	result := sqlsplitter.Split(`SELECT * FROM "my;table"; SELECT 2`)
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(result))
	}
}

func TestDollarQuoting(t *testing.T) {
	result := sqlsplitter.Split("SELECT $$hello; world$$; SELECT 2")
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(result))
	}
}

func TestLineComment(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1 -- ignore this ; semicolon\n; SELECT 2")
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(result), result)
	}
}

func TestBlockComment(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1 /* ignore ; this */ ; SELECT 2")
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(result))
	}
}

func TestTrailingSemicolon(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1;")
	if len(result) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(result))
	}
}

func TestNoSemicolons(t *testing.T) {
	result := sqlsplitter.Split("SELECT 1")
	if len(result) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(result))
	}
}

func TestWhitespaceOnly(t *testing.T) {
	result := sqlsplitter.Split("   \n\t  ")
	if len(result) != 0 {
		t.Fatalf("expected 0 statements, got %d", len(result))
	}
}

func TestMultiLine(t *testing.T) {
	sql := `
CREATE TABLE foo (
    id INT,
    name VARCHAR
);

INSERT INTO foo VALUES (1, 'hello');
`
	result := sqlsplitter.Split(sql)
	if len(result) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(result), result)
	}
}

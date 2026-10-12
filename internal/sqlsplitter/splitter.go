package sqlsplitter

type state int

const (
	stateNormal state = iota
	stateSingleQuote
	stateDoubleQuote
	stateDollarQuote
	stateLineComment
	stateBlockComment
)

func Split(sql string) []string {
	var statements []string
	var current []rune
	s := stateNormal
	runes := []rune(sql)
	dollarTag := ""

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		switch s {
		case stateNormal:
			switch {
			case c == '\'':
				s = stateSingleQuote
				current = append(current, c)
			case c == '"':
				s = stateDoubleQuote
				current = append(current, c)
			case c == '$' && i+1 < len(runes) && runes[i+1] == '$':
				s = stateDollarQuote
				dollarTag = "$$"
				current = append(current, '$', '$')
				i++
			case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
				s = stateLineComment
				current = append(current, '-', '-')
				i++
			case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
				s = stateBlockComment
				current = append(current, '/', '*')
				i++
			case c == ';':
				stmt := trim(string(current))
				if stmt != "" {
					statements = append(statements, stmt)
				}
				current = nil
			default:
				current = append(current, c)
			}

		case stateSingleQuote:
			current = append(current, c)
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current = append(current, '\'')
					i++
				} else {
					s = stateNormal
				}
			}

		case stateDoubleQuote:
			current = append(current, c)
			if c == '"' {
				s = stateNormal
			}

		case stateDollarQuote:
			current = append(current, c)
			dollarTag += string(c)
			if stringsSuffix(dollarTag, "$$") && len(dollarTag) > 2 {
				s = stateNormal
				dollarTag = ""
			}

		case stateLineComment:
			current = append(current, c)
			if c == '\n' {
				s = stateNormal
			}

		case stateBlockComment:
			current = append(current, c)
			if c == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				current = append(current, '/')
				i++
				s = stateNormal
			}
		}
	}

	stmt := trim(string(current))
	if stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}

func trim(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func stringsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

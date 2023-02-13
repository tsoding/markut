package main

import (
	"fmt"
	"unicode"
)

type Loc struct {
	FilePath string
	Row      int
	Col      int
}

type DiagErr struct {
	Loc Loc
	Err error
}

func (err *DiagErr) Error() string {
	return fmt.Sprintf("%s: ERROR: %s", err.Loc, err.Err)
}

func (loc Loc) String() string {
	return fmt.Sprintf("%s:%d:%d", loc.FilePath, loc.Row+1, loc.Col+1)
}

type Lexer struct {
	Content  []rune
	FilePath string
	Row      int
	Cur      int
	Bol      int
	PeekBuf  Token
	PeekFull bool
}

func NewLexer(content string, filePath string) Lexer {
	return Lexer{
		Content:  []rune(content),
		FilePath: filePath,
	}
}

type TokenKind int

const (
	TokenEOF TokenKind = iota
	TokenSymbol
	TokenString
	TokenBracketOpen
	TokenBracketClose
	TokenCurlyOpen
	TokenCurlyClose
	TokenParenOpen
	TokenParenClose
	TokenEllipsis
	TokenAsterisk
	TokenTimestamp
	TokenDash
	TokenPlus
)

var TokenKindName = map[TokenKind]string{
	TokenEOF:          "end of file",
	TokenSymbol:       "symbol",
	TokenString:       "string literal",
	TokenBracketOpen:  "open bracket",
	TokenBracketClose: "close bracket",
	TokenCurlyOpen:    "open curly",
	TokenCurlyClose:   "close curly",
	TokenParenOpen:    "open paren",
	TokenParenClose:   "close paren",
	TokenEllipsis:     "ellipsis",
	TokenAsterisk:     "asterisk",
	TokenTimestamp:    "timestamp",
	TokenDash:         "dash",
	TokenPlus:         "plus",
}

type LiteralToken struct {
	Text string
	Kind TokenKind
}

var LiteralTokens = []LiteralToken{
	{Text: "[", Kind: TokenBracketOpen},
	{Text: "]", Kind: TokenBracketClose},
	{Text: "{", Kind: TokenCurlyOpen},
	{Text: "}", Kind: TokenCurlyClose},
	{Text: "(", Kind: TokenParenOpen},
	{Text: ")", Kind: TokenParenClose},
	{Text: "...", Kind: TokenEllipsis},
	{Text: "*", Kind: TokenAsterisk},
	{Text: "-", Kind: TokenDash},
	{Text: "+", Kind: TokenPlus},
}

type Token struct {
	Kind   TokenKind
	Text   []rune
	Timestamp Secs
	Loc    Loc
}

func (lexer *Lexer) ChopChar() {
	if lexer.Cur >= len(lexer.Content) {
		return
	}
	x := lexer.Content[lexer.Cur];
	lexer.Cur += 1;
	if x == '\n' {
		lexer.Row += 1;
		lexer.Bol = lexer.Cur;
	}
}

func (lexer *Lexer) ChopChars(n int) {
	for lexer.Cur < len(lexer.Content) && n > 0 {
		lexer.ChopChar()
		n -= 1
	}
}

func (lexer *Lexer) DropLine() {
	for lexer.Cur < len(lexer.Content) && lexer.Content[lexer.Cur] != '\n' {
		lexer.ChopChar()
	}
	if lexer.Cur < len(lexer.Content) {
		lexer.ChopChar()
	}
}

func (lexer *Lexer) TrimLeft() {
	for lexer.Cur < len(lexer.Content) && unicode.IsSpace(lexer.Content[lexer.Cur]) {
		lexer.ChopChar()
	}
}

func (lexer *Lexer) Prefix(prefix []rune) bool {
	for i := range prefix {
		if lexer.Cur+i >= len(lexer.Content) {
			return false
		}
		if lexer.Content[lexer.Cur+i] != prefix[i] {
			return false
		}
	}
	return true
}

func (lexer *Lexer) Loc() Loc {
	return Loc{
		FilePath: lexer.FilePath,
		Row:      lexer.Row,
		Col:      lexer.Cur - lexer.Bol,
	}
}

func (lexer *Lexer) ChopHexByteValue() (result rune, err error) {
	for i := 0; i < 2; i += 1 {
		if lexer.Cur >= len(lexer.Content) {
			err = &DiagErr{
				Loc: lexer.Loc(),
				Err: fmt.Errorf("Unfinished hexadecimal value of a byte. Expected 2 hex digits, but got %d.", i),
			}
			return
		}
		x := lexer.Content[lexer.Cur]
		if '0' <= x && x <= '9' {
			result = result*0x10 + x - '0'
		} else if 'a' <= x && x <= 'f' {
			result = result*0x10 + x - 'a' + 10
		} else if 'A' <= x && x <= 'F' {
			result = result*0x10 + x - 'A' + 10
		} else {
			err = &DiagErr{
				Loc: lexer.Loc(),
				Err: fmt.Errorf("Expected hex digit, but got `%c`", x),
			}
			return
		}
		lexer.ChopChar()
	}
	return
}

func (lexer *Lexer) ChopStrLit() (lit []rune, err error) {
	if lexer.Cur >= len(lexer.Content) {
		return
	}

	quote := lexer.Content[lexer.Cur]
	lexer.ChopChar()
	begin := lexer.Cur

loop:
	for lexer.Cur < len(lexer.Content) {
		if lexer.Content[lexer.Cur] == '\\' {
			lexer.ChopChar()
			if lexer.Cur >= len(lexer.Content) {
				err = &DiagErr{
					Loc: lexer.Loc(),
					Err: fmt.Errorf("Unfinished escape sequence"),
				}
				return
			}

			switch lexer.Content[lexer.Cur] {
			case '0':
				lit = append(lit, 0)
				lexer.ChopChar();
			case 'n':
				lit = append(lit, '\n')
				lexer.ChopChar();
			case 'r':
				lit = append(lit, '\r')
				lexer.ChopChar()
			case '\\':
				lit = append(lit, '\\')
				lexer.ChopChar()
			case 'x':
				lexer.ChopChar()
				var value rune
				value, err = lexer.ChopHexByteValue()
				if err != nil {
					return
				}
				lit = append(lit, value)
			default:
				if lexer.Content[lexer.Cur] == quote {
					lit = append(lit, quote)
					lexer.ChopChar()
				} else {
					err = &DiagErr{
						Loc: lexer.Loc(),
						Err: fmt.Errorf("Unknown escape sequence starting with %c", lexer.Content[lexer.Cur]),
					}
					return
				}
			}
		} else {
			if lexer.Content[lexer.Cur] == quote {
				break loop
			}
			lit = append(lit, lexer.Content[lexer.Cur])
			lexer.ChopChar()
		}
	}

	if lexer.Cur >= len(lexer.Content) || lexer.Content[lexer.Cur] != quote {
		err = &DiagErr{
			Loc: Loc{
				FilePath: lexer.FilePath,
				Row:      lexer.Row,
				Col:      begin,
			},
			Err: fmt.Errorf("Expected '%c' at the end of this string literal", quote),
		}
		return
	}
	lexer.ChopChar()

	return
}

func IsSymbolStart(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func IsSymbol(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsNumber(ch) || ch == '_'
}

func IsTimestamp(ch rune) bool {
	return unicode.IsNumber(ch) || ch == ':' || ch == '.'
}

func (lexer *Lexer) ChopToken() (token Token, err error) {
	for lexer.Cur < len(lexer.Content) {
		lexer.TrimLeft()

		if lexer.Prefix([]rune("//")) {
			lexer.DropLine()
			continue
		}

		if lexer.Prefix([]rune("/*")) {
			for lexer.Cur < len(lexer.Content) && !lexer.Prefix([]rune("*/")) {
				lexer.ChopChar()
			}
			if lexer.Prefix([]rune("*/")) {
				lexer.ChopChars(2)
			}
			continue
		}

		break
	}

	token.Loc = lexer.Loc()

	if lexer.Cur >= len(lexer.Content) {
		return
	}

	if unicode.IsNumber(lexer.Content[lexer.Cur]) {
		token.Kind = TokenTimestamp
		begin := lexer.Cur

		for lexer.Cur < len(lexer.Content) && IsTimestamp(lexer.Content[lexer.Cur]) {
			lexer.ChopChar()
		}

		token.Text = lexer.Content[begin:lexer.Cur]
		token.Timestamp, err = tsToSecs(string(token.Text))
		if err != nil {
			err = &DiagErr{
				Loc: token.Loc,
				Err: fmt.Errorf("Invalid timestamp symbol: %w", err),
			}
		}
		return
	}

	if IsSymbolStart(lexer.Content[lexer.Cur]) {
		begin := lexer.Cur

		for lexer.Cur < len(lexer.Content) && IsSymbol(lexer.Content[lexer.Cur]) {
			lexer.ChopChar()
		}

		token.Kind = TokenSymbol
		token.Text = lexer.Content[begin:lexer.Cur]
		return
	}

	if lexer.Content[lexer.Cur] == '"' || lexer.Content[lexer.Cur] == '\'' {
		var lit []rune
		lit, err = lexer.ChopStrLit()
		if err != nil {
			return
		}
		token.Kind = TokenString
		token.Text = lit
		return
	}

	for i := range LiteralTokens {
		runeName := []rune(LiteralTokens[i].Text)
		if lexer.Prefix(runeName) {
			token.Kind = LiteralTokens[i].Kind
			token.Text = runeName
			lexer.ChopChars(len(runeName))
			return
		}
	}

	err = &DiagErr{
		Loc: lexer.Loc(),
		Err: fmt.Errorf("Invalid token"),
	}
	return
}

func (lexer *Lexer) Peek() (token Token, err error) {
	if !lexer.PeekFull {
		token, err = lexer.ChopToken()
		if err != nil {
			return
		}
		lexer.PeekFull = true
		lexer.PeekBuf = token
	} else {
		token = lexer.PeekBuf
	}
	return
}

func (lexer *Lexer) Next() (token Token, err error) {
	if lexer.PeekFull {
		token = lexer.PeekBuf
		lexer.PeekFull = false
		return
	}

	token, err = lexer.ChopToken()
	return
}

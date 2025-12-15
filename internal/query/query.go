package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type TokenKind int

const (
	tokEOF TokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokBool
	tokDuration
	tokLParen
	tokRParen
	tokEq
	tokNeq
	tokGt
	tokGte
	tokLt
	tokLte
	tokAnd
	tokOr
	tokPlus
	tokMinus
)

type Token struct {
	Kind TokenKind
	Text string
}

type lexer struct {
	s string
	i int
}

func lex(s string) ([]Token, error) {
	l := &lexer{s: s}
	var out []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		if tok.Kind == tokEOF {
			return out, nil
		}
	}
}

func (l *lexer) next() (Token, error) {
	for l.i < len(l.s) {
		c := l.s[l.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.i++
			continue
		}
		break
	}

	if l.i >= len(l.s) {
		return Token{Kind: tokEOF}, nil
	}

	c := l.s[l.i]
	// Operators / punctuation
	if strings.HasPrefix(l.s[l.i:], "&&") {
		l.i += 2
		return Token{Kind: tokAnd, Text: "&&"}, nil
	}
	if strings.HasPrefix(l.s[l.i:], "||") {
		l.i += 2
		return Token{Kind: tokOr, Text: "||"}, nil
	}
	if strings.HasPrefix(l.s[l.i:], "==") {
		l.i += 2
		return Token{Kind: tokEq, Text: "=="}, nil
	}
	if strings.HasPrefix(l.s[l.i:], "!=") {
		l.i += 2
		return Token{Kind: tokNeq, Text: "!="}, nil
	}
	if strings.HasPrefix(l.s[l.i:], ">=") {
		l.i += 2
		return Token{Kind: tokGte, Text: ">="}, nil
	}
	if strings.HasPrefix(l.s[l.i:], "<=") {
		l.i += 2
		return Token{Kind: tokLte, Text: "<="}, nil
	}
	if c == '>' {
		l.i++
		return Token{Kind: tokGt, Text: ">"}, nil
	}
	if c == '<' {
		l.i++
		return Token{Kind: tokLt, Text: "<"}, nil
	}
	if c == '(' {
		l.i++
		return Token{Kind: tokLParen, Text: "("}, nil
	}
	if c == ')' {
		l.i++
		return Token{Kind: tokRParen, Text: ")"}, nil
	}
	if c == '+' {
		l.i++
		return Token{Kind: tokPlus, Text: "+"}, nil
	}
	if c == '-' {
		l.i++
		return Token{Kind: tokMinus, Text: "-"}, nil
	}

	// Strings
	if c == '\'' || c == '"' {
		quote := c
		l.i++
		start := l.i
		for l.i < len(l.s) {
			if l.s[l.i] == quote {
				text := l.s[start:l.i]
				l.i++
				return Token{Kind: tokString, Text: text}, nil
			}
			l.i++
		}
		return Token{}, errors.New("unterminated string")
	}

	// Numbers / durations
	if c >= '0' && c <= '9' {
		start := l.i
		for l.i < len(l.s) {
			cc := l.s[l.i]
			if (cc >= '0' && cc <= '9') || cc == '.' {
				l.i++
				continue
			}
			break
		}
		mid := l.i
		for l.i < len(l.s) {
			cc := l.s[l.i]
			if (cc >= 'a' && cc <= 'z') || (cc >= 'A' && cc <= 'Z') {
				l.i++
				continue
			}
			break
		}
		text := l.s[start:l.i]
		if mid < l.i {
			return Token{Kind: tokDuration, Text: text}, nil
		}
		return Token{Kind: tokNumber, Text: text}, nil
	}

	// Identifiers
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
		start := l.i
		l.i++
		for l.i < len(l.s) {
			cc := l.s[l.i]
			if (cc >= 'a' && cc <= 'z') || (cc >= 'A' && cc <= 'Z') || (cc >= '0' && cc <= '9') || cc == '_' || cc == '.' {
				l.i++
				continue
			}
			break
		}
		text := l.s[start:l.i]
		if text == "true" || text == "false" {
			return Token{Kind: tokBool, Text: text}, nil
		}
		return Token{Kind: tokIdent, Text: text}, nil
	}

	return Token{}, fmt.Errorf("unexpected character: %q", c)
}

type node interface {
	eval(ctx map[string]any) (any, error)
}

type parser struct {
	toks []Token
	i    int
}

func Parse(expr string) (node, error) {
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != tokEOF {
		return nil, fmt.Errorf("unexpected token: %s", p.peek().Text)
	}
	return n, nil
}

func (p *parser) peek() Token {
	if p.i >= len(p.toks) {
		return Token{Kind: tokEOF}
	}
	return p.toks[p.i]
}

func (p *parser) eat(kind TokenKind) (Token, error) {
	t := p.peek()
	if t.Kind != kind {
		return Token{}, fmt.Errorf("expected %v, got %v (%q)", kind, t.Kind, t.Text)
	}
	p.i++
	return t, nil
}

func (p *parser) match(kind TokenKind) bool {
	if p.peek().Kind == kind {
		p.i++
		return true
	}
	return false
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tokOr {
		p.i++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binNode{op: tokOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tokAnd {
		p.i++
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = binNode{op: tokAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseCompare() (node, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	switch p.peek().Kind {
	case tokEq, tokNeq, tokGt, tokGte, tokLt, tokLte:
		op := p.peek().Kind
		p.i++
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return cmpNode{op: op, left: left, right: right}, nil
	default:
		return left, nil
	}
}

func (p *parser) parseAdd() (node, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().Kind
		if k != tokPlus && k != tokMinus {
			return left, nil
		}
		p.i++
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = arithNode{op: k, left: left, right: right}
	}
}

func (p *parser) parsePrimary() (node, error) {
	t := p.peek()
	switch t.Kind {
	case tokLParen:
		p.i++
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(tokRParen); err != nil {
			return nil, err
		}
		return n, nil
	case tokString:
		p.i++
		return litNode{v: t.Text}, nil
	case tokNumber:
		p.i++
		f, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			return nil, err
		}
		return litNode{v: f}, nil
	case tokDuration:
		p.i++
		d, err := parseDuration(t.Text)
		if err != nil {
			return nil, err
		}
		return litNode{v: d}, nil
	case tokBool:
		p.i++
		return litNode{v: t.Text == "true"}, nil
	case tokIdent:
		p.i++
		name := t.Text
		if p.match(tokLParen) {
			if _, err := p.eat(tokRParen); err != nil {
				return nil, err
			}
			return funcNode{name: name}, nil
		}
		return identNode{name: name}, nil
	default:
		return nil, fmt.Errorf("unexpected token: %v (%q)", t.Kind, t.Text)
	}
}

type litNode struct{ v any }

func (n litNode) eval(_ map[string]any) (any, error) { return n.v, nil }

type identNode struct{ name string }

func (n identNode) eval(ctx map[string]any) (any, error) {
	parts := strings.Split(n.name, ".")
	var cur any = ctx
	for _, p := range parts {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[p]
		default:
			return nil, nil
		}
	}
	return cur, nil
}

type funcNode struct{ name string }

func (n funcNode) eval(_ map[string]any) (any, error) {
	now := time.Now()
	switch n.name {
	case "now":
		return now, nil
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	case "tomorrow":
		y, m, d := now.Date()
		t := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		return t.Add(24 * time.Hour), nil
	default:
		return nil, fmt.Errorf("unknown function: %s", n.name)
	}
}

type binNode struct {
	op          TokenKind
	left, right node
}

func (n binNode) eval(ctx map[string]any) (any, error) {
	lv, err := n.left.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := n.right.eval(ctx)
	if err != nil {
		return nil, err
	}
	if n.op == tokAnd {
		lb, _ := toBool(lv)
		if !lb {
			return false, nil
		}
		rb, _ := toBool(rv)
		return lb && rb, nil
	}
	if n.op == tokOr {
		lb, _ := toBool(lv)
		if lb {
			return true, nil
		}
		rb, _ := toBool(rv)
		return lb || rb, nil
	}
	return nil, fmt.Errorf("unsupported boolean op")
}

type arithNode struct {
	op          TokenKind
	left, right node
}

func (n arithNode) eval(ctx map[string]any) (any, error) {
	lv, err := n.left.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := n.right.eval(ctx)
	if err != nil {
		return nil, err
	}
	lt, lok := toTime(lv)
	rd, dok := rv.(time.Duration)
	if lok && dok {
		if n.op == tokMinus {
			return lt.Add(-rd), nil
		}
		if n.op == tokPlus {
			return lt.Add(rd), nil
		}
	}
	lf, fok := toFloat(lv)
	rf, fok2 := toFloat(rv)
	if fok && fok2 {
		if n.op == tokMinus {
			return lf - rf, nil
		}
		if n.op == tokPlus {
			return lf + rf, nil
		}
	}
	return nil, errors.New("unsupported arithmetic")
}

type cmpNode struct {
	op          TokenKind
	left, right node
}

func (n cmpNode) eval(ctx map[string]any) (any, error) {
	lv, err := n.left.eval(ctx)
	if err != nil {
		return nil, err
	}
	rv, err := n.right.eval(ctx)
	if err != nil {
		return nil, err
	}

	// time compare
	lt, lok := toTime(lv)
	rt, rok := toTime(rv)
	if lok && rok {
		switch n.op {
		case tokEq:
			return lt.Equal(rt), nil
		case tokNeq:
			return !lt.Equal(rt), nil
		case tokGt:
			return lt.After(rt), nil
		case tokGte:
			return lt.After(rt) || lt.Equal(rt), nil
		case tokLt:
			return lt.Before(rt), nil
		case tokLte:
			return lt.Before(rt) || lt.Equal(rt), nil
		}
	}

	// numeric compare
	lf, fok := toFloat(lv)
	rf, fok2 := toFloat(rv)
	if fok && fok2 {
		switch n.op {
		case tokEq:
			return lf == rf, nil
		case tokNeq:
			return lf != rf, nil
		case tokGt:
			return lf > rf, nil
		case tokGte:
			return lf >= rf, nil
		case tokLt:
			return lf < rf, nil
		case tokLte:
			return lf <= rf, nil
		}
	}

	// string compare
	ls, sok := lv.(string)
	rs, sok2 := rv.(string)
	if sok && sok2 {
		switch n.op {
		case tokEq:
			return ls == rs, nil
		case tokNeq:
			return ls != rs, nil
		case tokGt:
			return ls > rs, nil
		case tokGte:
			return ls >= rs, nil
		case tokLt:
			return ls < rs, nil
		case tokLte:
			return ls <= rs, nil
		}
	}

	// bool compare
	lb, bok := toBool(lv)
	rb, bok2 := toBool(rv)
	if bok && bok2 {
		switch n.op {
		case tokEq:
			return lb == rb, nil
		case tokNeq:
			return lb != rb, nil
		}
	}

	return false, nil
}

func parseDuration(s string) (time.Duration, error) {
	// supports: 24h, 7d, 2w, 1m
	if s == "" {
		return 0, errors.New("empty duration")
	}
	unit := s[len(s)-1]
	num := s[:len(s)-1]
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	switch unit {
	case 'h':
		return time.Duration(f * float64(time.Hour)), nil
	case 'd':
		return time.Duration(f * float64(24*time.Hour)), nil
	case 'w':
		return time.Duration(f * float64(7*24*time.Hour)), nil
	case 'm':
		return time.Duration(f * float64(30*24*time.Hour)), nil
	default:
		return 0, fmt.Errorf("unknown duration unit: %q", unit)
	}
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		if t == "true" {
			return true, true
		}
		if t == "false" {
			return false, true
		}
	}
	return false, false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if t == "" {
			return time.Time{}, false
		}
		parsed, err := time.Parse(time.RFC3339, t)
		if err == nil {
			return parsed, true
		}
		parsed, err = time.Parse(time.RFC3339Nano, t)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func Filter(items []any, expr string) ([]any, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return items, nil
	}
	n, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, it := range items {
		ctx, ok := it.(map[string]any)
		if !ok {
			continue
		}
		v, err := n.eval(ctx)
		if err != nil {
			return nil, err
		}
		b, _ := toBool(v)
		if b {
			out = append(out, it)
		}
	}
	return out, nil
}

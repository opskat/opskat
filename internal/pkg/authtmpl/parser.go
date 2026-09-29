package authtmpl

import "strconv"

// parser 是一个简单的递归下降解析器，语法见包文档：
//
//	expr     := term ('+' term)*
//	term     := STRING | IDENT | IDENT '.' IDENT | IDENT '(' (expr (',' expr)*)? ')'
type parser struct {
	toks         []token
	pos          int
	fields       map[string]struct{}
	allowRequest bool
}

// parseExpr 解析一个 `{{ }}` 内部的表达式源码，同时完成所有静态校验：
// 未知字段、未知函数、参数个数、request.* 是否允许、语法错误。
func parseExpr(src string, fields map[string]struct{}, allowRequest bool) (exprNode, *ParseError) {
	toks, err := lexExpr(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, newParseError(CodeEmptyExpression, "empty expression")
	}
	p := &parser{toks: toks, fields: fields, allowRequest: allowRequest}
	node, err := p.parseAddExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, newParseError(CodeUnexpectedToken, "unexpected token after expression")
	}
	return node, nil
}

func (p *parser) peek() token {
	if p.pos >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.pos]
}

func (p *parser) next() token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *parser) parseAddExpr() (exprNode, *ParseError) {
	first, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	parts := []exprNode{first}
	for p.peek().kind == tokPlus {
		p.next()
		t, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		parts = append(parts, t)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return concatNode{parts: parts}, nil
}

func (p *parser) parseTerm() (exprNode, *ParseError) {
	tok := p.peek()
	switch tok.kind {
	case tokString:
		p.next()
		return literalNode{value: []byte(tok.text)}, nil
	case tokIdent:
		p.next()
		name := tok.text
		switch p.peek().kind {
		case tokDot:
			p.next()
			attrTok := p.peek()
			if attrTok.kind != tokIdent {
				return nil, newParseError(CodeExpectedIdentifier, "expected identifier after "+strconv.Quote(name+"."), "after", name+".")
			}
			p.next()
			return p.buildContextNode(name, attrTok.text)
		case tokLParen:
			p.next()
			return p.parseFuncCall(name)
		default:
			if _, ok := p.fields[name]; !ok {
				return nil, newParseError(CodeUnknownField, "unknown field "+strconv.Quote(name), "name", name)
			}
			return fieldNode{name: name}, nil
		}
	default:
		return nil, newParseError(CodeUnexpectedToken, "unexpected token in expression")
	}
}

func (p *parser) buildContextNode(root, attr string) (exprNode, *ParseError) {
	ref := root + "." + attr
	switch root {
	case "now":
		switch attr {
		case "unix", "unix_ms":
			return nowNode{attr: attr}, nil
		default:
			return nil, newParseError(CodeUnknownReference, "unknown now attribute "+strconv.Quote(attr), "ref", ref)
		}
	case "request":
		if !p.allowRequest {
			return nil, newParseError(CodeRequestNotAllowed, "request.* is not allowed in this context")
		}
		switch attr {
		case "method", "path", "body":
			return requestNode{attr: attr}, nil
		default:
			return nil, newParseError(CodeUnknownReference, "unknown request attribute "+strconv.Quote(attr), "ref", ref)
		}
	default:
		return nil, newParseError(CodeUnknownReference, "unknown reference "+strconv.Quote(ref), "ref", ref)
	}
}

func (p *parser) parseFuncCall(name string) (exprNode, *ParseError) {
	var args []exprNode
	if p.peek().kind != tokRParen {
		for {
			arg, err := p.parseAddExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.peek().kind == tokComma {
				p.next()
				continue
			}
			break
		}
	}
	if p.peek().kind != tokRParen {
		return nil, newParseError(CodeMissingCloseParen, "expected ')' in call to "+name+"(...)", "name", name)
	}
	p.next()

	spec, ok := builtinFuncs[name]
	if !ok {
		return nil, newParseError(CodeUnknownFunction, "unknown function "+strconv.Quote(name), "name", name)
	}
	if spec.arity != len(args) {
		want, got := strconv.Itoa(spec.arity), strconv.Itoa(len(args))
		return nil, newParseError(CodeWrongArgCount, "function "+strconv.Quote(name)+" expects "+want+" argument(s), got "+got,
			"name", name, "want", want, "got", got)
	}
	return funcCallNode{name: name, args: args, spec: spec}, nil
}

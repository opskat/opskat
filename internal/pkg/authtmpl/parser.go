package authtmpl

import "fmt"

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
func parseExpr(src string, fields map[string]struct{}, allowRequest bool) (exprNode, error) {
	toks, err := lexExpr(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	p := &parser{toks: toks, fields: fields, allowRequest: allowRequest}
	node, err := p.parseAddExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected token after expression")
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

func (p *parser) parseAddExpr() (exprNode, error) {
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

func (p *parser) parseTerm() (exprNode, error) {
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
				return nil, fmt.Errorf("expected identifier after %q", name+".")
			}
			p.next()
			return p.buildContextNode(name, attrTok.text)
		case tokLParen:
			p.next()
			return p.parseFuncCall(name)
		default:
			if _, ok := p.fields[name]; !ok {
				return nil, fmt.Errorf("unknown field %q", name)
			}
			return fieldNode{name: name}, nil
		}
	default:
		return nil, fmt.Errorf("unexpected token in expression")
	}
}

func (p *parser) buildContextNode(root, attr string) (exprNode, error) {
	switch root {
	case "now":
		switch attr {
		case "unix", "unix_ms":
			return nowNode{attr: attr}, nil
		default:
			return nil, fmt.Errorf("unknown now attribute %q", attr)
		}
	case "request":
		if !p.allowRequest {
			return nil, fmt.Errorf("request.* is not allowed in this context")
		}
		switch attr {
		case "method", "path", "body":
			return requestNode{attr: attr}, nil
		default:
			return nil, fmt.Errorf("unknown request attribute %q", attr)
		}
	default:
		return nil, fmt.Errorf("unknown reference %q.%q", root, attr)
	}
}

func (p *parser) parseFuncCall(name string) (exprNode, error) {
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
		return nil, fmt.Errorf("expected ')' in call to %s(...)", name)
	}
	p.next()

	spec, ok := builtinFuncs[name]
	if !ok {
		return nil, fmt.Errorf("unknown function %q", name)
	}
	if spec.arity != len(args) {
		return nil, fmt.Errorf("function %q expects %d argument(s), got %d", name, spec.arity, len(args))
	}
	return funcCallNode{name: name, args: args, spec: spec}, nil
}

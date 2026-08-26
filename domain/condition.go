package domain

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ConditionEnv is the runtime context for job `if` expressions.
// Trigger `where` filters are a separate mechanism.
type ConditionEnv struct {
	Event   Event
	Jobs    map[string]JobRun
	Outputs map[string]any
}

// EvalIf evaluates a small expression language: booleans, ==, !=,
// dotted paths, and subject_contains("..."). Empty expression is true.
func EvalIf(expr string, env ConditionEnv) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	p := &ifParser{s: expr}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	p.skip()
	if p.pos < len(p.s) {
		return false, fmt.Errorf("trailing input at %q", p.s[p.pos:])
	}
	return truthy(evalValue(v, env)), nil
}

type ifParser struct {
	s   string
	pos int
}

func (p *ifParser) skip() {
	for p.pos < len(p.s) && unicode.IsSpace(rune(p.s[p.pos])) {
		p.pos++
	}
}

func (p *ifParser) parseOr() (ifNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skip()
		if strings.HasPrefix(p.s[p.pos:], "||") {
			p.pos += 2
			right, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			left = ifBin{"||", left, right}
			continue
		}
		return left, nil
	}
}

func (p *ifParser) parseAnd() (ifNode, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for {
		p.skip()
		if strings.HasPrefix(p.s[p.pos:], "&&") {
			p.pos += 2
			right, err := p.parseCmp()
			if err != nil {
				return nil, err
			}
			left = ifBin{"&&", left, right}
			continue
		}
		return left, nil
	}
}

func (p *ifParser) parseCmp() (ifNode, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	p.skip()
	op := ""
	switch {
	case strings.HasPrefix(p.s[p.pos:], "=="):
		op = "=="
		p.pos += 2
	case strings.HasPrefix(p.s[p.pos:], "!="):
		op = "!="
		p.pos += 2
	default:
		return left, nil
	}
	right, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return ifBin{op, left, right}, nil
}

func (p *ifParser) parsePrimary() (ifNode, error) {
	p.skip()
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	if p.s[p.pos] == '(' {
		p.pos++
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return nil, fmt.Errorf("missing )")
		}
		p.pos++
		return n, nil
	}
	if p.s[p.pos] == '"' {
		s, err := p.parseQuote('"')
		if err != nil {
			return nil, err
		}
		return ifLit{s}, nil
	}
	if p.s[p.pos] == '\'' {
		s, err := p.parseQuote('\'')
		if err != nil {
			return nil, err
		}
		return ifLit{s}, nil
	}
	start := p.pos
	if unicode.IsDigit(rune(p.s[p.pos])) || p.s[p.pos] == '-' {
		p.pos++
		for p.pos < len(p.s) && (unicode.IsDigit(rune(p.s[p.pos])) || p.s[p.pos] == '.') {
			p.pos++
		}
		return ifLit{p.s[start:p.pos]}, nil
	}
	if unicode.IsLetter(rune(p.s[p.pos])) || p.s[p.pos] == '_' {
		p.pos++
		for p.pos < len(p.s) && (unicode.IsLetter(rune(p.s[p.pos])) || unicode.IsDigit(rune(p.s[p.pos])) || p.s[p.pos] == '_' || p.s[p.pos] == '.') {
			p.pos++
		}
		name := p.s[start:p.pos]
		p.skip()
		if p.pos < len(p.s) && p.s[p.pos] == '(' {
			p.pos++
			arg, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			p.skip()
			if p.pos >= len(p.s) || p.s[p.pos] != ')' {
				return nil, fmt.Errorf("missing ) in call")
			}
			p.pos++
			return ifCall{name, arg}, nil
		}
		return ifIdent{name}, nil
	}
	return nil, fmt.Errorf("unexpected %q", p.s[p.pos:])
}

func (p *ifParser) parseQuote(q byte) (string, error) {
	if p.pos >= len(p.s) || p.s[p.pos] != q {
		return "", fmt.Errorf("expected quote")
	}
	p.pos++
	var b strings.Builder
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		p.pos++
		if c == q {
			return b.String(), nil
		}
		if c == '\\' && p.pos < len(p.s) {
			b.WriteByte(p.s[p.pos])
			p.pos++
			continue
		}
		b.WriteByte(c)
	}
	return "", fmt.Errorf("unterminated string")
}

type ifNode interface{ ifNode() }

type ifLit struct{ v string }
type ifIdent struct{ name string }
type ifBin struct {
	op          string
	left, right ifNode
}
type ifCall struct {
	name string
	arg  ifNode
}

func (ifLit) ifNode()   {}
func (ifIdent) ifNode() {}
func (ifBin) ifNode()   {}
func (ifCall) ifNode()  {}

func evalValue(n ifNode, env ConditionEnv) any {
	switch t := n.(type) {
	case ifLit:
		return t.v
	case ifIdent:
		return resolveIdent(t.name, env)
	case ifCall:
		arg := fmt.Sprint(evalValue(t.arg, env))
		switch t.name {
		case "event.subject_contains", "subject_contains":
			return strings.Contains(strings.ToLower(env.Event.Subject), strings.ToLower(arg))
		default:
			return false
		}
	case ifBin:
		l := evalValue(t.left, env)
		r := evalValue(t.right, env)
		switch t.op {
		case "&&":
			return truthy(l) && truthy(r)
		case "||":
			return truthy(l) || truthy(r)
		case "==":
			return stringify(l) == stringify(r)
		case "!=":
			return stringify(l) != stringify(r)
		}
	}
	return nil
}

func resolveIdent(name string, env ConditionEnv) any {
	switch name {
	case "true":
		return "true"
	case "false":
		return "false"
	}
	if strings.HasPrefix(name, "event.") {
		return lookupAttr(env.Event, strings.TrimPrefix(name, "event."))
	}
	if jr, ok := env.Jobs[name]; ok {
		return string(jr.Status)
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		head, tail := name[:i], name[i+1:]
		if jr, ok := env.Jobs[head]; ok {
			switch tail {
			case "exit_code":
				return strconv.Itoa(jr.ExitCode)
			case "run_status":
				return string(jr.Status)
			case "status":
				if jr.Outputs != nil {
					if v, ok := jr.Outputs["status"]; ok {
						return v
					}
				}
				return string(jr.Status)
			}
			if jr.Outputs != nil {
				if v, ok := jr.Outputs[tail]; ok {
					return v
				}
			}
		}
		if env.Outputs != nil {
			if v, ok := env.Outputs[name]; ok {
				return v
			}
		}
	}
	if env.Outputs != nil {
		if v, ok := env.Outputs[name]; ok {
			return v
		}
	}
	return nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	default:
		s := stringify(t)
		return s != "" && s != "false" && s != "0"
	}
}

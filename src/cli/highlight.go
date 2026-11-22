package cli

import (
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"
)

const (
	inputCommand  = "\x1b[4;36m"
	inputOption   = "\x1b[36m"
	inputString   = "\x1b[33m"
	inputOperator = "\x1b[35m"
	inputComment  = "\x1b[38;5;242m"
)

// inputPainter decorates shell syntax for display only; it never looks up host paths or commands.
type inputPainter struct{ parser *syntax.Parser }

// Paint adds color and underline without changing the text or its visible width.
func (p inputPainter) Paint(line []rune, _ int) []rune {
	if len(line) == 0 || len(line) > 8192 {
		return line
	}
	text := string(line)
	tree, _ := p.parser.Parse(strings.NewReader(text), "")
	styles := make([]string, len(text))
	mark := func(start uint, size uint, color string) {
		if start > uint(len(text)) || size > uint(len(text))-start {
			return
		}
		for i := start; i < start+size; i++ {
			styles[i] = color
		}
	}
	node := func(n syntax.Node, color string) {
		start, end := n.Pos(), n.End()
		if start.IsValid() && end.IsValid() && end.Offset() >= start.Offset() {
			mark(start.Offset(), end.Offset()-start.Offset(), color)
		}
	}
	syntax.Walk(tree, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			for i, word := range n.Args {
				if i == 0 {
					node(word, inputCommand)
				} else if offset := word.Pos().Offset(); offset < uint(len(text)) && text[offset] == '-' {
					node(word, inputOption)
				}
			}
		case *syntax.SglQuoted, *syntax.DblQuoted:
			node(n, inputString)
		case *syntax.BinaryCmd:
			mark(n.OpPos.Offset(), uint(len(n.Op.String())), inputOperator)
		case *syntax.Redirect:
			mark(n.OpPos.Offset(), uint(len(n.Op.String())), inputOperator)
		case *syntax.Comment:
			node(n, inputComment)
		}
		return true
	})
	var painted []rune
	previous := ""
	offset := 0
	for _, r := range line {
		color := styles[offset]
		if color != previous {
			if previous != "" {
				painted = append(painted, []rune(colorReset)...)
			}
			painted = append(painted, []rune(color)...)
			previous = color
		}
		painted = append(painted, r)
		width := utf8.RuneLen(r)
		if width < 0 {
			width = utf8.RuneLen(utf8.RuneError)
		}
		offset += width
	}
	if previous != "" {
		painted = append(painted, []rune(colorReset)...)
	}
	return painted
}

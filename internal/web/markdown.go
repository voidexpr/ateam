package web

import (
	"bytes"
	"html"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

type chromaRenderer struct{}

func (r *chromaRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gast.KindFencedCodeBlock, r.renderFencedCode)
}

func (r *chromaRenderer) renderFencedCode(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}
	n := node.(*gast.FencedCodeBlock)

	lang := "text"
	if n.Info != nil {
		info := n.Info.Segment.Value(source)
		if idx := bytes.IndexAny(info, " \t\r\n"); idx >= 0 {
			info = info[:idx]
		}
		if len(info) > 0 {
			lang = string(info)
		}
	}

	var code bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		code.Write(line.Value(source))
	}

	if err := quick.Highlight(w, code.String(), lang, "html", "dracula"); err != nil {
		if _, err := w.WriteString("<pre><code>"); err != nil {
			return gast.WalkStop, err
		}
		if _, err := w.WriteString(html.EscapeString(code.String())); err != nil {
			return gast.WalkStop, err
		}
		if _, err := w.WriteString("</code></pre>"); err != nil {
			return gast.WalkStop, err
		}
	}

	return gast.WalkSkipChildren, nil
}

type chromaExtension struct{}

func (e *chromaExtension) Extend(m goldmark.Markdown) {
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&chromaRenderer{}, 200),
	))
}

func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			&chromaExtension{},
		),
	)
}

func (s *Server) renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := s.md.Convert([]byte(src), &buf); err != nil {
		return "<pre>" + html.EscapeString(src) + "</pre>"
	}
	return buf.String()
}

package web

import (
	"bytes"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithStyle("dracula"),
			),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}

func (s *Server) renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := s.md.Convert([]byte(src), &buf); err != nil {
		return "<pre>" + src + "</pre>"
	}
	return buf.String()
}

package markup

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	htmlRenderer "github.com/yuin/goldmark/renderer/html"
)

var wikiLinkRegex = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func RenderMarkdownToHTML(raw string) (string, error) {
	return RenderMarkdownWithWikiLinks(raw, nil)
}

func RenderMarkdownWithWikiLinks(raw string, noteExists func(title string) (bool, error)) (string, error) {
	processed := wikiLinkRegex.ReplaceAllStringFunc(raw, func(value string) string {
		sub := wikiLinkRegex.FindStringSubmatch(value)
		if len(sub) < 2 {
			return value
		}
		title := strings.TrimSpace(sub[1])
		if title == "" {
			return value
		}
		href := "/notes/view?title=" + url.QueryEscape(title)
		classes := "wiki-link"
		if noteExists != nil {
			exists, err := noteExists(title)
			if err == nil && !exists {
				classes += " is-ghost"
				href = "/notes?create=" + url.QueryEscape(title)
			}
		}
		return fmt.Sprintf(`<a class="%s" href="%s">[[%s]]</a>`, classes, href, html.EscapeString(title))
	})

	var buf bytes.Buffer
	md := goldmark.New(
		goldmark.WithRendererOptions(htmlRenderer.WithUnsafe()),
	)
	if err := md.Convert([]byte(processed), &buf); err != nil {
		return "", err
	}
	sanitizer := bluemonday.UGCPolicy()
	sanitized := sanitizer.SanitizeBytes(buf.Bytes())
	return strings.TrimSpace(html.UnescapeString(string(sanitized))), nil
}

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

func ParseWikiLinks(content string) []string {
	matches := wikiLinkRegex.FindAllStringSubmatch(content, -1)
	titles := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			title := strings.TrimSpace(match[1])
			if title != "" {
				titles = append(titles, title)
			}
		}
	}
	return titles
}

var tagRegex = regexp.MustCompile(`#([a-zA-Z0-9_]+)`)

func ParseTags(content string) []string {
	matches := tagRegex.FindAllStringSubmatch(content, -1)
	tagSet := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			tag := strings.ToLower(strings.TrimSpace(match[1]))
			if tag != "" {
				tagSet[tag] = true
			}
		}
	}
	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}
	return tags
}

func RenderMarkdownToHTML(raw string) (string, error) {
	return RenderMarkdownWithWikiLinks(raw, nil)
}

func RenderMarkdownWithWikiLinks(raw string, noteResolver func(title string) (id string, exists bool, err error)) (string, error) {
	processed := wikiLinkRegex.ReplaceAllStringFunc(raw, func(value string) string {
		sub := wikiLinkRegex.FindStringSubmatch(value)
		if len(sub) < 2 {
			return value
		}
		title := strings.TrimSpace(sub[1])
		if title == "" {
			return value
		}
		classes := "wiki-link"
		var href string
		if noteResolver != nil {
			id, exists, err := noteResolver(title)
			if err != nil {
				return value
			}
			if exists {
				href = "/notes/" + id
			} else {
				href = "/notes?create=" + url.QueryEscape(title)
				classes += " is-ghost"
			}
		} else {
			href = "/notes/view?title=" + url.QueryEscape(title)
		}
		return fmt.Sprintf(`<a class="%s" href="%s">%s</a>`, classes, href, html.EscapeString(title))
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

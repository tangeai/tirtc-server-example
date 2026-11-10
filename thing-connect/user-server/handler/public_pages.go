package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/boardcatalog"
	"thing-connect/internal/sitebrand"
)

const publicMetadata = `
<meta name="robots" content="index,follow,max-image-preview:large">
<meta property="og:type" content="website">
<meta property="og:locale" content="zh_CN">
<meta property="og:site_name" content="{{.Site.Name}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta name="twitter:card" content="summary">
<meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{.Description}}">
{{if .Canonical}}<link rel="canonical" href="{{.Canonical}}">
<meta property="og:url" content="{{.Canonical}}">
<meta property="og:image" content="{{.Image}}">
<meta property="og:image:alt" content="{{.ImageAlt}}">
<meta name="twitter:image" content="{{.Image}}">{{end}}
<script type="application/ld+json">{{.Structured}}</script>`

const homeBoardCards = `{{range .Boards}}<article class="catalog-card"><div class="product-art">{{if .ImageURL}}<img src="{{.ImageURL}}" alt="{{.Name}} 产品图" loading="lazy">{{else}}<span class="board-image-fallback">{{.Chip}}</span>{{end}}</div><div class="catalog-info"><small>{{.Vendor}} · {{.Chip}}</small><h3>{{if .DetailSlug}}<a href="/boards/{{.DetailSlug}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</h3><div class="catalog-actions">{{if .FirmwareURL}}<a class="primary" href="{{.FirmwareURL}}" target="_blank" rel="noopener noreferrer">下载固件 ↓</a>{{end}}{{if .FlashingGuideURL}}<a class="guide-link" href="{{.FlashingGuideURL}}" target="_blank" rel="noopener noreferrer">烧录指南 ↗</a>{{end}}</div></div></article>{{end}}`

// Public pages render the same catalog to people and crawlers. Only presentation
// belongs here; publication/filtering stays in the catalog business module.
func RegisterPublicPages(r *gin.Engine, staticDir string, site *sitebrand.State, catalog *boardcatalog.Service) error {
	templates := map[string]*template.Template{}
	titleRE := regexp.MustCompile(`(?s)<title[^>]*>.*?</title>`)
	descriptionRE := regexp.MustCompile(`(?s)<meta\s+name="description"[^>]*>`)
	brandRE := regexp.MustCompile(`(?s)(<span[^>]*data-i18n="site.brand"[^>]*>).*?(</span>)`)
	homeGridRE := regexp.MustCompile(`(?s)(<div[^>]*id="home-boards"[^>]*>).*?(</div>)`)
	routes := map[string]string{"/": "index.html", "/boards": "boards.html", "/getting-started": "public-guide.html", "/faq": "public-guide.html", "/boards/:slug": "public-guide.html"}
	tutorials := make(map[string]*publicTutorial, len(publicTutorials))
	for i := range publicTutorials {
		tutorial := &publicTutorials[i]
		routes[tutorial.Path] = "public-guide.html"
		tutorials[tutorial.Path] = tutorial
	}
	for route, file := range routes {
		raw, err := os.ReadFile(filepath.Join(staticDir, file))
		if err != nil {
			return err
		}
		text := string(raw)
		text = titleRE.ReplaceAllString(text, `<title data-site-page="`+file+`">{{.Title}}</title>`)
		text = descriptionRE.ReplaceAllString(text, `<meta name="description" content="{{.Description}}">`)
		text = brandRE.ReplaceAllString(text, `${1}{{.Site.Name}}${2}`)
		text = strings.Replace(text, "<!-- public:metadata -->", publicMetadata, 1)
		text = strings.Replace(text, "<!-- public:bootstrap -->", `<script id="site-brand-data" type="application/json">{{.SiteJSON}}</script><script id="public-catalog-data" type="application/json">{{.CatalogJSON}}</script>`, 1)
		if route == "/" {
			text = homeGridRE.ReplaceAllStringFunc(text, func(match string) string {
				parts := homeGridRE.FindStringSubmatch(match)
				return strings.Replace(parts[1], `aria-busy="true"`, `aria-busy="false"`, 1) + homeBoardCards + parts[2]
			})
			// Static preview fallback must not say "loading" after server-side rendering.
			stateRE := regexp.MustCompile(`(?s)<p id="home-board-state"[^>]*>.*?</p>`)
			text = stateRE.ReplaceAllString(text, `<p id="home-board-state" role="status" {{if .Boards}}hidden{{end}}>{{if .CatalogError}}开发板加载失败，请重试。{{else}}暂无已上架开发板{{end}}</p>`)
		} else if route == "/boards" {
			// The catalog remains useful without JavaScript, including resource links.
			text = strings.Replace(text, `<section id="featured-list" class="featured-list" aria-label="开发板" data-i18n-aria-label="site.boards"></section>`, `<section id="featured-list" class="featured-list" aria-label="开发板" data-i18n-aria-label="site.boards"></section><noscript><div class="catalog-static">`+homeBoardCards+`</div></noscript>`, 1)
		}
		parsed, err := template.New(file).Parse(text)
		if err != nil {
			return err
		}
		templates[route] = parsed
	}
	for path, tpl := range templates {
		route, view := path, tpl
		r.Match([]string{http.MethodGet, http.MethodHead}, route, func(c *gin.Context) {
			current := site.Current()
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			var boards []boardcatalog.Board
			var board *boardcatalog.Board
			var err error
			if route == "/" || route == "/boards" {
				boards, err = catalog.List(ctx)
			} else if route == "/boards/:slug" {
				value, lookupErr := catalog.BySlug(ctx, c.Param("slug"))
				if lookupErr != nil {
					c.Header("X-Robots-Tag", "noindex")
					c.Header("Cache-Control", "no-store")
					if errors.Is(lookupErr, boardcatalog.ErrNotFound) {
						c.String(http.StatusNotFound, "开发板未上架或不存在，请返回开发板目录查看。")
					} else {
						slog.WarnContext(c.Request.Context(), "public board unavailable")
						c.Header("Retry-After", "60")
						c.String(http.StatusServiceUnavailable, "开发板暂时无法加载，请稍后重试。")
					}
					return
				}
				board = &value
			}
			if err != nil {
				slog.WarnContext(c.Request.Context(), "public catalog unavailable")
				boards = []boardcatalog.Board{}
			}
			if boards == nil {
				boards = []boardcatalog.Board{}
			}
			if route == "/" && len(boards) > 3 {
				boards = boards[:3]
			}
			title := "开源 AI 音视频方案 | " + current.Name
			description := current.Name + "提供 AI 语音对话、设备与微信通话、设备互呼及多人对讲、远程实时音视频。选择开发板下载固件，联网绑定即可体验；提供服务端、Web 与微信小程序源码。"
			websiteDescription := description
			if route == "/boards" {
				title = "开发板与固件 | " + current.Name
				description = "查看" + current.Name + "已上架开发板的型号、适配状态、固件下载与烧录指南。"
			}
			heading := ""
			var sections []publicSection
			switch route {
			case "/getting-started":
				heading = "设备音视频与 AI 接入指南"
				description = "从电脑模拟器、开发板固件到自有硬件与自建服务，按阶段验证设备音视频与 AI 交互。"
				sections = gettingStartedSections
			case "/faq":
				heading = "设备接入常见问题"
				description = "了解小钛与 TiRTC 的关系、模拟器体验、固件选择、微信 VoIP 前提和量产适配边界。"
				sections = faqSections
			case "/boards/:slug":
				heading = board.Name + " · 固件与接入资源"
				description = board.Summary
			}
			tutorial := tutorials[route]
			if tutorial != nil {
				heading, description = tutorial.Title, tutorial.Description
			}
			if heading != "" {
				title = heading + " | " + current.Name
			}
			pagePath := route
			if board != nil {
				pagePath = "/boards/" + board.DetailSlug
			}
			canonical, image, imageAlt := "", "", current.Name
			boardUpdated := ""
			if board != nil && !board.UpdatedAt.IsZero() {
				boardUpdated = board.UpdatedAt.UTC().Format(time.RFC3339)
			}
			if current.PublicURL != "" {
				canonical = strings.TrimRight(current.PublicURL, "/") + pagePath
				image = strings.TrimRight(current.PublicURL, "/") + "/static/xiaotai-logo-transparent.png"
				if board != nil && board.ImageURL != "" {
					image, imageAlt = board.ImageURL, board.Name+" 产品图"
					if strings.HasPrefix(image, "/") {
						image = strings.TrimRight(current.PublicURL, "/") + image
					}
				}
			}
			graph := []any{map[string]any{"@type": "WebSite", "name": current.Name, "description": websiteDescription, "inLanguage": "zh-CN"}}
			if canonical != "" {
				graph[0].(map[string]any)["url"] = strings.TrimRight(current.PublicURL, "/") + "/"
			}
			if current.NameEN != "" {
				graph[0].(map[string]any)["alternateName"] = current.NameEN
			}
			page := map[string]any{"@type": "WebPage", "name": title, "description": description, "inLanguage": "zh-CN"}
			if canonical != "" {
				page["url"] = canonical
			}
			if boardUpdated != "" {
				page["dateModified"] = boardUpdated
			}
			if tutorial != nil {
				page["@type"] = "TechArticle"
				page["about"] = tutorial.Audience
			}
			if route == "/faq" {
				page["@type"] = "FAQPage"
				questions := make([]any, 0, len(sections))
				for _, section := range sections {
					questions = append(questions, map[string]any{"@type": "Question", "name": section.Heading, "acceptedAnswer": map[string]any{"@type": "Answer", "text": section.Text}})
				}
				page["mainEntity"] = questions
			}
			if canonical != "" {
				base := strings.TrimRight(current.PublicURL, "/")
				page["@id"] = canonical + "#webpage"
				page["isPartOf"] = map[string]any{"@id": base + "/#website"}
				graph[0].(map[string]any)["@id"] = base + "/#website"
				if route != "/" {
					crumbs := []any{map[string]any{"@type": "ListItem", "position": 1, "name": "首页", "item": base + "/"}}
					if tutorial != nil {
						crumbs = append(crumbs, map[string]any{"@type": "ListItem", "position": 2, "name": "接入指南", "item": base + "/getting-started"})
					}
					if board != nil {
						crumbs = append(crumbs, map[string]any{"@type": "ListItem", "position": 2, "name": "开发板", "item": base + "/boards"})
					}
					name := heading
					if name == "" {
						name = "开发板"
					}
					crumbs = append(crumbs, map[string]any{"@type": "ListItem", "position": len(crumbs) + 1, "name": name, "item": canonical})
					graph = append(graph, map[string]any{"@type": "BreadcrumbList", "itemListElement": crumbs})
				}
			}
			graph = append(graph, page)
			structured, _ := json.Marshal(map[string]any{"@context": "https://schema.org", "@graph": graph})
			siteJSON, _ := json.Marshal(current)
			status := "ready"
			if err != nil {
				status = "error"
			} else if len(boards) == 0 {
				status = "empty"
			}
			catalogJSON, _ := json.Marshal(map[string]any{"boards": boards, "status": status})
			data := struct {
				Site                                  sitebrand.Public
				Title, Description, Canonical, Image  string
				Heading, Kind, ImageAlt, BoardUpdated string
				Tutorial                              *publicTutorial
				Tutorials                             []publicTutorial
				Sections                              []publicSection
				Board                                 *boardcatalog.Board
				Boards                                []boardcatalog.Board
				CatalogError                          bool
				Structured, SiteJSON, CatalogJSON     template.JS
			}{Site: current, Title: title, Description: description, Canonical: canonical, Image: image, Heading: heading, Kind: route, ImageAlt: imageAlt, BoardUpdated: boardUpdated, Tutorial: tutorial, Tutorials: publicTutorials, Sections: sections, Board: board, Boards: boards, CatalogError: err != nil, Structured: template.JS(structured), SiteJSON: template.JS(siteJSON), CatalogJSON: template.JS(catalogJSON)} // #nosec: json.Marshal escapes HTML/script delimiters; never raw input.
			var out bytes.Buffer
			if err := view.Execute(&out, data); err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Content-Type-Options", "nosniff")
			c.Header("Content-Length", strconv.Itoa(out.Len()))
			if c.Request.Method == http.MethodHead {
				c.Data(http.StatusOK, "text/html; charset=utf-8", nil)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", out.Bytes())
		})
	}
	r.Match([]string{http.MethodGet, http.MethodHead}, "/robots.txt", func(c *gin.Context) {
		value := "User-agent: *\nAllow: /\nDisallow: /v1/\nAllow: /v1/board-images/\nDisallow: /admin/\n"
		if base := site.Current().PublicURL; base != "" {
			value += "\nSitemap: " + strings.TrimRight(base, "/") + "/sitemap.xml\n"
		}
		c.Header("Cache-Control", "no-cache")
		if c.Request.Method == http.MethodHead {
			c.Data(http.StatusOK, "text/plain; charset=utf-8", nil)
			return
		}
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(value))
	})
	r.Match([]string{http.MethodGet, http.MethodHead}, "/sitemap.xml", func(c *gin.Context) {
		base := strings.TrimRight(site.Current().PublicURL, "/")
		if base == "" {
			c.Header("X-Robots-Tag", "noindex")
			c.Status(http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		boards, err := catalog.List(ctx)
		if err != nil {
			slog.WarnContext(c.Request.Context(), "sitemap catalog unavailable")
			c.Header("Retry-After", "60")
			c.Header("Cache-Control", "no-store")
			c.Header("X-Robots-Tag", "noindex")
			c.Status(http.StatusServiceUnavailable)
			return
		}
		type location struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod,omitempty"`
		}
		payload := struct {
			XMLName xml.Name   `xml:"urlset"`
			NS      string     `xml:"xmlns,attr"`
			URLs    []location `xml:"url"`
		}{NS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: []location{{Loc: base + "/"}, {Loc: base + "/boards"}, {Loc: base + "/getting-started"}, {Loc: base + "/faq"}}}
		for _, tutorial := range publicTutorials {
			payload.URLs = append(payload.URLs, location{Loc: base + tutorial.Path})
		}
		for _, board := range boards {
			if board.DetailSlug != "" {
				entry := location{Loc: base + "/boards/" + board.DetailSlug}
				if !board.UpdatedAt.IsZero() {
					entry.LastMod = board.UpdatedAt.UTC().Format(time.RFC3339)
				}
				payload.URLs = append(payload.URLs, entry)
			}
		}
		raw, err := xml.Marshal(payload)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Header("Cache-Control", "no-cache")
		if c.Request.Method == http.MethodHead {
			c.Data(http.StatusOK, "application/xml; charset=utf-8", nil)
			return
		}
		c.Data(http.StatusOK, "application/xml; charset=utf-8", append([]byte(xml.Header), raw...))
	})
	return nil
}

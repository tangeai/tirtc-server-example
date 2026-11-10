import importlib.util
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

spec = importlib.util.spec_from_file_location("public_site", Path(__file__).with_name("check-public-site.py"))
site = importlib.util.module_from_spec(spec)
spec.loader.exec_module(site)


def html(url):
    return ('<!doctype html><title>站点</title><meta name="description" content="说明">'
            '<link href="' + url + '" rel="canonical">'
            '<meta property="og:url" content="' + url + '">'
            '<meta property="og:image" content="https://example.test/image.png">'
            '<script type="application/ld+json">{"@context":"https://schema.org","@type":"WebPage"}</script>'
            '<main><h1>教程</h1><p>' + '真实可读正文。' * 12 + '</p></main>')


class PageTests(unittest.TestCase):
    def test_attribute_order_and_structured_data(self):
        self.assertEqual(site.validate_page(html("https://example.test/"), "https://example.test/", {"Content-Type": "text/html; charset=utf-8"}), [])

    def test_rejects_noindex_and_wrong_origin(self):
        errors = site.validate_page(html("https://wrong.test/"), "https://example.test/", {"X-Robots-Tag": "noindex, nofollow"})
        self.assertTrue(any("canonical" in error for error in errors))
        self.assertTrue(any("noindex" in error for error in errors))

    def test_crawler_specific_noindex(self):
        url = "https://example.test/"
        for name in ("googlebot", "bingbot"):
            with self.subTest(name=name):
                errors = site.validate_page(html(url), url, {"Content-Type":"text/html", "X-Robots-Tag":name + ": noindex"})
                self.assertTrue(any("noindex" in error for error in errors))
                errors = site.validate_page(html(url) + '<meta name="' + name + '" content="noindex">', url, {"Content-Type":"text/html"})
                self.assertTrue(any("noindex" in error for error in errors))

    def test_rejects_plain_text_page(self):
        errors = site.validate_page(html("https://example.test/"), "https://example.test/", {"Content-Type":"text/plain"})
        self.assertTrue(any("Content-Type" in error for error in errors))

    def test_scripts_do_not_count_as_visible_content(self):
        page = site.Page('<main><h1>标题</h1><script>' + 'x' * 1000 + '</script></main>')
        self.assertEqual(page.main_text, "标题")


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.missing_robots = False
        self.foreign_redirect = False
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                if owner.foreign_redirect and self.path == "/boards":
                    self.send_response(302)
                    self.send_header("Location", "https://foreign.invalid/")
                    self.end_headers()
                    return
                if self.path == "/robots.txt":
                    if owner.missing_robots:
                        self.send_error(404)
                        return
                    body = "User-agent: *\nAllow: /\nSitemap: " + owner.base + "/sitemap.xml\n"
                elif self.path == "/sitemap.xml":
                    body = '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">' + ''.join('<url><loc>' + owner.base + path + '</loc></url>' for path in site.REQUIRED_PATHS) + '</urlset>'
                else:
                    body = html(owner.base + self.path)
                self.send_response(200)
                content_type = "text/plain" if self.path == "/robots.txt" else "application/xml" if self.path == "/sitemap.xml" else "text/html"
                self.send_header("Content-Type", content_type)
                if self.path in ("/login", "/devices"):
                    self.send_header("X-Robots-Tag", "noindex, nofollow")
                self.end_headers()
                self.wfile.write(body.encode())

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.base = "http://127.0.0.1:" + str(self.server.server_port)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.thread.join()
        self.server.server_close()

    def test_checks_public_and_private_pages(self):
        report = site.audit(self.base)
        self.assertTrue(report["passed"], report)
        self.assertEqual(len(report["checks"]), len(site.REQUIRED_PATHS) + 4)

    def test_reports_missing_robots_but_keeps_checking_pages(self):
        self.missing_robots = True
        report = site.audit(self.base)
        self.assertFalse(report["passed"])
        self.assertIn("HTTP 404", report["checks"][0]["errors"])
        self.assertTrue(any(row["path"] == "/faq" and not row["errors"] for row in report["checks"]))

    def test_does_not_follow_cross_origin_redirect(self):
        self.foreign_redirect = True
        report = site.audit(self.base)
        board = next(row for row in report["checks"] if row["path"] == "/boards")
        self.assertTrue(any("跨站" in error for error in board["errors"]))
        self.assertFalse(report["passed"])


if __name__ == "__main__":
    unittest.main()

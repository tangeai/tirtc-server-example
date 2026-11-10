#!/usr/bin/env python3
"""Read-only HTTP acceptance checks for public SEO pages; no account required."""
import argparse
import json
import re
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
import urllib.robotparser
import xml.etree.ElementTree as ET
from html.parser import HTMLParser
from pathlib import Path

REQUIRED_PATHS = ("/", "/boards", "/getting-started", "/faq", "/guides/simulator",
                  "/guides/wechat-voip", "/guides/linux-device")
MAX_BYTES = 2_000_000


class Page(HTMLParser):
    def __init__(self, body):
        super().__init__(convert_charrefs=True)
        self.canonicals, self.jsonld, self.meta = [], [], {}
        self.robot_directives = []
        self.title, self.main_text, self.h1 = "", "", 0
        self.in_title = self.in_main = self.in_script = self.in_style = False
        self.script_type, self.script = "", ""
        self.feed(body)

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "link" and "canonical" in a.get("rel", "").split():
            self.canonicals.append(a.get("href", ""))
        if tag == "meta":
            name = a.get("name", a.get("property", "")).lower()
            self.meta[name] = a.get("content", "")
            if name in ("robots", "googlebot", "bingbot"):
                self.robot_directives.append(a.get("content", ""))
        if tag == "title":
            self.in_title = True
        if tag == "main":
            self.in_main = True
        if tag == "h1":
            self.h1 += 1
        if tag == "script":
            self.in_script, self.script_type, self.script = True, a.get("type", ""), ""
        if tag == "style":
            self.in_style = True

    def handle_endtag(self, tag):
        if tag == "title":
            self.in_title = False
        if tag == "main":
            self.in_main = False
        if tag == "style":
            self.in_style = False
        if tag == "script":
            if self.script_type == "application/ld+json":
                self.jsonld.append(self.script)
            self.in_script = False

    def handle_data(self, data):
        if self.in_script:
            self.script += data
            return
        if self.in_style:
            return
        if self.in_title:
            self.title += data
        if self.in_main:
            self.main_text += data


def validate_page(body, url, headers):
    page = Page(body)
    errors = []
    if headers.get("Content-Type", "").partition(";")[0].strip().lower() not in ("text/html", "application/xhtml+xml"):
        errors.append("页面响应不是 HTML Content-Type")
    if page.canonicals != [url]:
        errors.append("canonical 缺失、重复或与正式地址不一致")
    if not page.title.strip() or not page.meta.get("description", "").strip():
        errors.append("缺少标题或描述")
    if page.h1 != 1 or len(page.main_text.strip()) < 40:
        errors.append("原始 HTML 缺少单一主标题或可读正文")
    header_directives = headers.get_all("X-Robots-Tag", []) if hasattr(headers, "get_all") else [headers.get("X-Robots-Tag", "")]
    directives = ",".join(page.robot_directives + header_directives)
    if re.search(r"\b(?:noindex|none)\b", directives, re.IGNORECASE):
        errors.append("公开页被设置为 noindex")
    if page.meta.get("og:url") != url or not page.meta.get("og:image"):
        errors.append("分享地址或分享图片不完整")
    if not page.jsonld:
        errors.append("缺少 JSON-LD")
    for raw in page.jsonld:
        try:
            value = json.loads(raw)
            if not isinstance(value, dict) or value.get("@context") != "https://schema.org":
                errors.append("JSON-LD 缺少预期的 schema.org context")
        except ValueError:
            errors.append("JSON-LD 不是有效 JSON")
    return errors


class SameOriginRedirect(urllib.request.HTTPRedirectHandler):
    def __init__(self, origin):
        self.origin = origin

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        p = urllib.parse.urlsplit(newurl)
        if (p.scheme, p.netloc) != self.origin:
            raise ValueError("拒绝跟随跨站重定向")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def audit(base, timeout=10, ca_file=None):
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/"):
        raise ValueError("base-url 必须是无凭证、无路径参数的 HTTP(S) 站点根地址")
    base = base.rstrip("/")
    opener = urllib.request.build_opener(SameOriginRedirect((parsed.scheme, parsed.netloc)),
                                          urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=ca_file)))
    results = []

    def fetch(path):
        row = {"path": path, "errors": []}
        results.append(row)
        started = time.monotonic()
        try:
            request = urllib.request.Request(base + path, headers={"User-Agent": "ThingConnect-PublicSiteCheck/1.0"})
            with opener.open(request, timeout=timeout) as response:
                body = response.read(MAX_BYTES + 1)
                row.update(status=response.status, bytes=len(body))
                if response.status != 200:
                    row["errors"].append("预期 HTTP 200，实际 " + str(response.status))
                if not body.strip():
                    row["errors"].append("响应正文为空")
                if len(body) > MAX_BYTES:
                    raise ValueError("响应超过检查器的 2 MB 上限")
                if response.geturl() != base + path:
                    row["errors"].append("请求被重定向，请检查正式路径")
                return row, body.decode("utf-8", "replace"), response.headers
        except (OSError, ValueError, urllib.error.URLError) as error:
            row["errors"].append("HTTP " + str(error.code) if isinstance(error, urllib.error.HTTPError) else type(error).__name__ + ": " + str(error))
            return row, "", {}
        finally:
            row["elapsed_ms"] = round((time.monotonic() - started) * 1000)

    robots_row, robots, robots_headers = fetch("/robots.txt")
    if robots and robots_headers.get("Content-Type", "").partition(";")[0].strip().lower() != "text/plain":
        robots_row["errors"].append("robots 响应不是 text/plain")
    robot_rules = urllib.robotparser.RobotFileParser()
    robot_rules.parse(robots.splitlines())
    if robots and base + "/sitemap.xml" not in (robot_rules.site_maps() or []):
        robots_row["errors"].append("robots 未声明本站 sitemap.xml")
    sitemap_row, sitemap, sitemap_headers = fetch("/sitemap.xml")
    if sitemap and sitemap_headers.get("Content-Type", "").partition(";")[0].strip().lower() not in ("application/xml", "text/xml"):
        sitemap_row["errors"].append("站点地图响应不是 XML Content-Type")
    paths = list(REQUIRED_PATHS)
    if sitemap:
        try:
            root = ET.fromstring(sitemap)
            if root.tag != "{http://www.sitemaps.org/schemas/sitemap/0.9}urlset":
                raise ValueError("预期 sitemap urlset")
            urls = [node.text or "" for node in root.findall("{*}url/{*}loc")]
            if len(urls) != len(set(urls)):
                sitemap_row["errors"].append("站点地图存在重复地址")
            for required in REQUIRED_PATHS:
                if base + required not in urls:
                    sitemap_row["errors"].append("站点地图缺少 " + required)
            if len(urls) > 200:
                raise ValueError("站点地图超过 200 个地址，请分批检查")
            for url in urls:
                item = urllib.parse.urlsplit(url)
                if (item.scheme, item.netloc) != (parsed.scheme, parsed.netloc) or item.query or item.fragment:
                    sitemap_row["errors"].append("站点地图包含非本站规范地址")
                elif item.path not in paths:
                    paths.append(item.path)
        except (ET.ParseError, ValueError) as error:
            sitemap_row["errors"].append("站点地图无效：" + str(error))
    for path in paths:
        row, body, headers = fetch(path)
        if body:
            row["errors"].extend(validate_page(body, base + path, headers))
        if robots and any(not robot_rules.can_fetch(bot, base + path) for bot in ("Googlebot", "Bingbot")):
            row["errors"].append("robots 阻止抓取")
    for path in ("/login", "/devices"):
        row, body, headers = fetch(path)
        if body:
            directives = headers.get("X-Robots-Tag", "") + "," + Page(body).meta.get("robots", "")
            if "noindex" not in directives.lower():
                row["errors"].append("用户页面缺少 noindex")
    return {"base_url": base, "passed": all(not r["errors"] for r in results), "checks": results}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--timeout", type=float, default=10)
    parser.add_argument("--json-output", type=Path)
    parser.add_argument("--ca-file", help="可选的测试站点 CA PEM 文件；仍验证证书和主机名")
    args = parser.parse_args()
    try:
        report = audit(args.base_url, args.timeout, args.ca_file)
    except ValueError as error:
        parser.error(str(error))
    for row in report["checks"]:
        print(("PASS " if not row["errors"] else "FAIL ") + row["path"] + " · " + ("；".join(row["errors"]) or "HTTP/HTML 检查通过"))
    print("响应时间仅是本次 HTTP 观测，不代表 Core Web Vitals、收录或 AI 引用效果。")
    if args.json_output:
        args.json_output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

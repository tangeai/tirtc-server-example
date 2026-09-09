#!/usr/bin/env python3
"""Check API Reference against business route declarations and document structure.

Run from any directory: python3 thing-connect/scripts/check-api-docs.py
No network, service credentials, or third-party Python packages are required.
This checks route coverage and documentation structure, not runtime semantics.
"""
import argparse
from collections import Counter
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
METHODS = r"GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS"


def registered_routes():
    """Read literal Gin registrations; reject unrecognized route expressions.

    The only supported dynamic form is a suffix from a literal []string loop.
    Fail instead of silently ignoring a new registration pattern.
    """
    routes = set()
    files = sorted(ROOT.glob("*-server/handler/*.go"))
    files.append(ROOT / "user-server/service_discovery.go")
    for path in files:
        if path.name.endswith("_test.go"):
            continue
        groups = {}
        loops = {}
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if line.startswith("func "):
                groups = dict.fromkeys(re.findall(r"(\w+) \*gin\.Engine", line), "")
                loops = {}
            line = line.split("//", 1)[0]
            loop = re.search(r'for _, (\w+) := range \[\]string\{([^}]+)\}', line)
            if loop:
                loops[loop[1]] = re.findall(r'"([^"]+)"', loop[2])
            group = re.search(r'(\w+) := (\w+)\.Group\("([^"]*)"', line)
            if group:
                if group[2] not in groups:
                    raise ValueError(f"{path}:{number}: unknown Gin group {group[2]}")
                groups[group[1]] = groups[group[2]] + group[3]
            call = re.search(rf'(\w+)\.({METHODS})\((.*)', line)
            if not call:
                continue
            receiver, method, args = call.groups()
            literal = re.match(r'"([^"]*)"\s*(?:\+\s*(\w+)\s*)?,', args)
            if receiver not in groups or not literal:
                raise ValueError(f"{path}:{number}: unsupported route declaration: {line.strip()}")
            suffixes = [""] if not literal[2] else loops.get(literal[2])
            if not suffixes:
                raise ValueError(f"{path}:{number}: unresolved route suffix")
            for suffix in suffixes:
                route = (method, groups[receiver] + literal[1] + suffix)
                if route in routes:
                    raise ValueError(f"Duplicate route: {route}")
                routes.add(route)
    return routes


def check(document):
    errors = []
    text = document.read_text()
    # Strip fenced examples before interpreting headings and tables.
    fence = re.compile(r'^(`{3,}|~{3,})([^\n]*)\n(.*?)^\1\s*$', re.M | re.S)
    for match in fence.finditer(text):
        if match[2].strip() == "json":
            try:
                json.loads(match[3])
            except ValueError as exc:
                errors.append(f"Invalid JSON example: {exc}")
    prose = fence.sub("", text)
    indexes = {}
    for line in prose.splitlines():
        if not re.match(r'^\| \[.*\| (?:' + METHODS + r') \|', line):
            continue
        cells = [cell.strip() for cell in line.strip('|').split('|')]
        if len(cells) != 5:
            errors.append(f"API index must have five columns: {line}")
            continue
        name, caller, method, path, auth = cells
        key = (method, path.strip('`'))
        if key in indexes:
            errors.append(f"Duplicate index entry: {key}")
        indexes[key] = (caller, auth)
        if not caller or not auth:
            errors.append(f"Missing caller/authentication: {key}")
        if '/internal/' in key[1] and (caller != '内部服务' or 'X-Internal-Key' not in auth):
            errors.append(f"Internal interface classification/authentication: {key}")
        if caller == '设备' and '用户 JWT' in auth:
            errors.append(f"Device interface incorrectly uses user JWT: {key}")
    details = {}
    pattern = re.compile(r'\*\*接口\*\*：`(' + METHODS + r') ([^`]+)`')
    matches = list(pattern.finditer(prose))
    for i, match in enumerate(matches):
        key = (match[1], match[2].split('?', 1)[0])
        if key in details:
            errors.append(f"Duplicate endpoint detail: {key}")
        body = prose[match.end():matches[i+1].start() if i+1 < len(matches) else len(prose)]
        caller = re.match(r'\s*\*\*调用方\*\*：([^。\n]+)。', body)
        details[key] = caller[1] if caller else None
        if key in indexes and details[key] != indexes[key][0]:
            errors.append(f"Index/detail caller differs: {key}")
        for param in re.findall(r':(\w+)', key[1]):
            if not re.search(r'\|\s*`?' + re.escape(param) + r'`?\s*\|', body):
                errors.append(f"Missing path parameter table: {key}: {param}")
    code = registered_routes()
    for label, left, right in [('Code route without detail', code, details),
                               ('Documented route absent from code', details, code),
                               ('Index without detail', indexes, details),
                               ('Detail without index', details, indexes)]:
        for key in sorted(set(left) - set(right)):
            errors.append(f"{label}: {key}")
    explicit = re.findall(r'<a id="([^"]+)"', prose)
    for anchor, count in Counter(explicit).items():
        if count > 1:
            errors.append(f"Duplicate anchor: {anchor}")
    anchors = set(explicit)
    seen = Counter()
    for heading in re.findall(r'^#{1,6} (.+)$', prose, re.M):
        slug = re.sub(r'[^\w\- ]', '', heading.lower()).replace(' ', '-')
        anchors.add(slug + (f'-{seen[slug]}' if seen[slug] else ''))
        seen[slug] += 1
        if 'UserJWTAuth' in heading or '鉴权同' in heading or '成功响应' in heading:
            errors.append(f"Implementation/response text in navigation: {heading}")
    for link in re.findall(r'\]\(([^)]+)\)', prose):
        if link.startswith('#') and link[1:] not in anchors:
            errors.append(f"Broken anchor: {link}")
        elif not re.match(r'\w+:|#|/', link):
            if not (document.parent / link.split('#', 1)[0]).exists():
                errors.append(f"Missing linked file: {link}")
    for line in re.finditer(r'^[^\n]+\n(?:---+|===+)\s*$', prose, re.M):
        errors.append(f"Accidental Setext heading (add blank line): {line[0]}")
    return errors, len(code), len(details)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--document', type=Path, default=ROOT / 'api-reference.md')
    args = parser.parse_args()
    try:
        errors, routes, details = check(args.document)
    except (ValueError, OSError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    if errors:
        print('\n'.join('FAIL: ' + error for error in errors), file=sys.stderr)
        return 1
    print(f'PASS: {routes} registered routes, {details} endpoint details; indexes, callers, path parameters, JSON examples and links checked.')
    print('Runtime authentication, validation and response semantics require contract tests and code review.')
    return 0


if __name__ == '__main__':
    sys.exit(main())

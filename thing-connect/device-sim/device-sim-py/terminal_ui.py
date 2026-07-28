#!/usr/bin/env python3
"""Small terminal formatting helpers for simulator command output."""

import shutil
import unicodedata


YELLOW = "\033[1;33m"
RESET = "\033[0m"


def _display_width(text: str) -> int:
    return sum(
        2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
        for char in text
    )


def _wrap_display_line(text: str, max_width: int) -> list[str]:
    """Wrap one line without losing long IDs and while preserving indentation."""
    if _display_width(text) <= max_width:
        return [text]
    indent = text[:len(text) - len(text.lstrip(" "))]
    rows = []
    current = ""
    current_width = 0
    for char in text:
        char_width = _display_width(char)
        if current and current_width + char_width > max_width:
            rows.append(current.rstrip())
            current = indent
            current_width = _display_width(indent)
        current += char
        current_width += char_width
    if current or not rows:
        rows.append(current.rstrip())
    return rows


def format_box(title: str, lines, max_content_width: int | None = None) -> list[str]:
    """Return an aligned Unicode box, accounting for CJK character widths."""
    rows = [str(line) for line in lines] or ["（空）"]
    if max_content_width is not None:
        max_content_width = max(_display_width(title), max_content_width)
        rows = [
            wrapped
            for row in rows
            for wrapped in _wrap_display_line(row, max_content_width)
        ]
    title_width = _display_width(title)
    content_width = max(title_width + 1, *(_display_width(row) for row in rows))
    top = f"╔═ {title} " + "═" * (content_width - title_width - 1) + "╗"
    body = [
        f"║ {row}{' ' * (content_width - _display_width(row))} ║"
        for row in rows
    ]
    bottom = "╚" + "═" * (content_width + 2) + "╝"
    return [top, *body, bottom]


def print_box(title: str, lines, prefix: str = "[contacts]") -> None:
    """Print a highlighted, terminal-width-aware box at any log level."""
    terminal_columns = shutil.get_terminal_size(fallback=(120, 24)).columns
    prefix_width = _display_width(prefix) + 1
    box_overhead = 4  # "║ " + " ║"
    available_content = terminal_columns - prefix_width - box_overhead
    max_content_width = max(
        _display_width(title),
        min(96, available_content),
    )
    for row in format_box(title, lines, max_content_width=max_content_width):
        print(f"{YELLOW}{prefix} {row}{RESET}", flush=True)

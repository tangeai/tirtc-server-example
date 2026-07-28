import unittest

from terminal_ui import _display_width, format_box


class TerminalUITests(unittest.TestCase):
    def test_long_values_wrap_inside_equal_width_box(self):
        box = format_box(
            "联系人列表（共 1 条）",
            ["    wx_open_id=" + "x" * 80],
            max_content_width=32,
        )

        self.assertGreater(len(box), 3)
        widths = [_display_width(row) for row in box]
        self.assertEqual(len(set(widths)), 1)
        self.assertLessEqual(widths[0], 36)
        self.assertEqual(
            "".join(row[2:-2].strip() for row in box[1:-1]).replace(" ", ""),
            "wx_open_id=" + "x" * 80,
        )

    def test_cjk_rows_are_aligned(self):
        box = format_box(
            "联系人",
            ["remark=刘德华1", "remark=guest"],
            max_content_width=40,
        )

        widths = [_display_width(row) for row in box]
        self.assertEqual(len(set(widths)), 1)


if __name__ == "__main__":
    unittest.main()

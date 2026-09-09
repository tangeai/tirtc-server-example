"""Regression tests: the documentation checker must reject contract drift."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest

SPEC = importlib.util.spec_from_file_location('api_docs', Path(__file__).with_name('check-api-docs.py'))
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class APIDocsCheckTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.original = (CHECKER.ROOT / 'api-reference.md').read_text()

    def errors_for(self, text):
        document = SimpleNamespace(read_text=lambda: text, parent=CHECKER.ROOT)
        return CHECKER.check(document)[0]

    def test_current_contract(self):
        self.assertEqual(self.errors_for(self.original), [])

    def test_wrong_path(self):
        text = self.original.replace('**接口**：`GET /services`', '**接口**：`GET /wrong-services`')
        self.assertTrue(any('absent from code' in e for e in self.errors_for(text)))

    def test_missing_endpoint(self):
        text = self.original.replace('**接口**：`GET /services`', '**接口**：省略')
        self.assertTrue(any('without detail' in e for e in self.errors_for(text)))

    def test_caller_mismatch(self):
        text = self.original.replace('**调用方**：设备、Web、小程序。', '**调用方**：设备。', 1)
        self.assertTrue(any('caller differs' in e for e in self.errors_for(text)))

    def test_broken_link(self):
        self.assertTrue(any('Broken anchor' in e for e in self.errors_for(self.original + '\n[错误链接](#not-an-anchor)\n')))

    def test_invalid_json(self):
        self.assertTrue(any('Invalid JSON' in e for e in self.errors_for(self.original + '\n```json\n{"broken":}\n```\n')))

    def test_accidental_heading(self):
        self.assertTrue(any('Setext' in e for e in self.errors_for(self.original + '\n成功响应文字\n---\n')))

    def test_duplicate_index(self):
        row = next(line for line in self.original.splitlines() if line.startswith('| [获取服务地址]'))
        self.assertTrue(any('Duplicate index' in e for e in self.errors_for(self.original + '\n' + row)))


if __name__ == '__main__':
    unittest.main()

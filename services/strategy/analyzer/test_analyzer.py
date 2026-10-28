import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("analyzer_server", Path(__file__).with_name("server.py"))
server = importlib.util.module_from_spec(spec)
spec.loader.exec_module(server)
check_spec = importlib.util.spec_from_file_location("analyzer_check", Path(__file__).with_name("check.py"))
check = importlib.util.module_from_spec(check_spec)
check_spec.loader.exec_module(check)


class AnalyzerTests(unittest.TestCase):
    def setUp(self):
        self.config = server.load_config(Path(__file__).with_name("config.json"))

    def test_real_python_syntax_and_scope_errors(self):
        for source in ("def broken(:\n pass", "return 3", "break", "if True:\nprint(1)"):
            result = server.analyze(source, self.config)
            self.assertFalse(result["valid"])
            self.assertEqual(result["diagnostics"][0]["severity"], "error")
            self.assertGreaterEqual(result["diagnostics"][0]["line"], 1)
            self.assertGreaterEqual(result["diagnostics"][0]["column"], 1)
        self.assertTrue(server.analyze("", self.config)["valid"])
        self.assertTrue(server.analyze("def run(data):\n    return data\n", self.config)["valid"])

    def test_code_is_never_executed_or_imported(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "must-not-exist"
            source = f"import os\nopen({str(marker)!r}, 'w').write('executed')\nraise RuntimeError('no')\nwhile True: pass\n"
            self.assertTrue(server.analyze(source, self.config)["valid"])
            self.assertFalse(marker.exists())

    def test_monaco_utf16_column(self):
        source = "x = '😀'; )"
        with self.assertRaises(SyntaxError) as caught:
            compile(source, "<strategy>", "exec")
        exc = caught.exception
        expected = len(source[:exc.offset - 1].encode("utf-16-le")) // 2 + 1
        self.assertEqual(check.diagnose(source)["diagnostics"][0]["column"], expected)


if __name__ == "__main__":
    unittest.main()

"""Trusted, one-shot Python compiler frontend. Never execute the compiled code."""
import json
import resource
import sys
import warnings


def diagnose(source):
    try:
        # Unlike ast.parse alone, this also rejects return/break outside a scope.
        # The code object is discarded: no eval/exec, imports, or user hooks run.
        with warnings.catch_warnings():
            warnings.simplefilter("ignore")
            compile(source, "<strategy>", "exec", dont_inherit=True, optimize=0)
        return {"valid": True, "diagnostics": []}
    except SyntaxError as exc:
        lines = source.split("\n")
        line = min(max(exc.lineno or 1, 1), len(lines))
        end_line = min(max(exc.end_lineno or line, line), len(lines))

        def column(row, offset):
            text = lines[row - 1]
            offset = min(max((offset or 1) - 1, 0), len(text))
            return sum(2 if ord(char) > 0xFFFF else 1 for char in text[:offset]) + 1

        start = column(line, exc.offset)
        end = column(end_line, exc.end_offset)
        if end_line == line:
            end = max(end, start)
        return {"valid": False, "diagnostics": [{
            "line": line, "column": start, "end_line": end_line,
            "end_column": end, "severity": "error", "message": exc.msg[:500],
        }]}


def main():
    payload = json.load(sys.stdin)
    limits = payload["limits"]
    resource.setrlimit(resource.RLIMIT_AS, (limits["memory_bytes"],) * 2)
    resource.setrlimit(resource.RLIMIT_CPU, (limits["cpu_seconds"],) * 2)
    resource.setrlimit(resource.RLIMIT_FSIZE, (0, 0))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_NOFILE, (32, 32))
    source = payload["source"]
    if len(source.encode("utf-8")) > limits["max_source_bytes"] or "\0" in source:
        raise ValueError("invalid source")
    result = diagnose(source)
    sys.stdout.write(json.dumps(result, ensure_ascii=True))


if __name__ == "__main__":
    main()

"""Minimal YAML subset reader. Stdlib only — policy.yaml is block-style.

Supported: comments, nested maps, block lists, flow `[]` / `{}`,
quoted strings, ints, bools, null. Not a general YAML implementation.
"""

from __future__ import annotations


class YamlError(ValueError):
    pass


def load_path(path: str) -> object:
    with open(path, "r", encoding="utf-8") as fh:
        return loads(fh.read())


def loads(text: str) -> object:
    lines: list[tuple[int, int, str]] = []
    for lineno, raw in enumerate(text.splitlines(), 1):
        stripped = _strip_comment(raw)
        if not stripped.strip():
            continue
        indent = len(stripped) - len(stripped.lstrip(" "))
        if stripped.lstrip(" ").startswith("\t"):
            raise YamlError(f"line {lineno}: tabs not allowed")
        lines.append((lineno, indent, stripped.strip()))
    if not lines:
        return {}
    value, next_i = _parse_block(lines, 0, lines[0][1])
    if next_i != len(lines):
        raise YamlError(f"line {lines[next_i][0]}: unexpected content")
    return value


def _strip_comment(raw: str) -> str:
    in_single = False
    in_double = False
    out = []
    i = 0
    while i < len(raw):
        ch = raw[i]
        if ch == "'" and not in_double:
            in_single = not in_single
            out.append(ch)
        elif ch == '"' and not in_single:
            in_double = not in_double
            out.append(ch)
        elif ch == "#" and not in_single and not in_double:
            break
        else:
            out.append(ch)
        i += 1
    return "".join(out).rstrip()


def _parse_block(lines: list[tuple[int, int, str]], i: int, indent: int):
    if i >= len(lines):
        return {}, i
    _, ind, content = lines[i]
    if ind < indent:
        return {}, i
    if content.startswith("- "):
        return _parse_list(lines, i, indent)
    return _parse_map(lines, i, indent)


def _parse_map(lines, i, indent):
    result: dict = {}
    while i < len(lines):
        lineno, ind, content = lines[i]
        if ind < indent:
            break
        if ind > indent:
            raise YamlError(f"line {lineno}: unexpected indent")
        if content.startswith("- "):
            raise YamlError(f"line {lineno}: list item in mapping")
        if ":" not in content:
            raise YamlError(f"line {lineno}: expected key:")
        key, _, rest = content.partition(":")
        key = key.strip()
        rest = rest.strip()
        if not key:
            raise YamlError(f"line {lineno}: empty key")
        i += 1
        if rest == "":
            if i < len(lines) and lines[i][1] > indent:
                value, i = _parse_block(lines, i, lines[i][1])
            else:
                value = None
        else:
            value = _parse_scalar(rest, lineno)
        result[key] = value
    return result, i


def _parse_list(lines, i, indent):
    result: list = []
    while i < len(lines):
        lineno, ind, content = lines[i]
        if ind < indent:
            break
        if ind > indent:
            raise YamlError(f"line {lineno}: unexpected indent")
        if not content.startswith("- "):
            break
        rest = content[2:].strip()
        i += 1
        if rest == "":
            if i < len(lines) and lines[i][1] > indent:
                value, i = _parse_block(lines, i, lines[i][1])
            else:
                value = None
        elif ": " in rest and not rest.startswith(("{", "[", "'", '"')):
            key, _, val = rest.partition(":")
            value = {key.strip(): _parse_scalar(val.strip(), lineno)}
            # continuation keys at deeper indent belong to this item
            if i < len(lines) and lines[i][1] > indent:
                extra, i = _parse_map(lines, i, lines[i][1])
                if not isinstance(extra, dict):
                    raise YamlError(f"line {lineno}: expected mapping continuation")
                value.update(extra)
        else:
            value = _parse_scalar(rest, lineno)
        result.append(value)
    return result, i


def _parse_scalar(s: str, lineno: int):
    if s == "[]":
        return []
    if s == "{}":
        return {}
    if s in ("null", "~"):
        return None
    if s in ("true", "True"):
        return True
    if s in ("false", "False"):
        return False
    if len(s) >= 2 and s[0] == s[-1] and s[0] in ("'", '"'):
        inner = s[1:-1]
        if s[0] == '"':
            return inner.encode("utf-8").decode("unicode_escape")
        return inner
    if s.startswith("[") and s.endswith("]"):
        inner = s[1:-1].strip()
        if not inner:
            return []
        parts = [p.strip() for p in inner.split(",")]
        return [_parse_scalar(p, lineno) for p in parts]
    if (s.isdigit() or (s[0] == "-" and s[1:].isdigit())) and s != "-":
        return int(s)
    if ":" in s and not s.startswith("/"):
        # reject accidental unparsed maps
        pass
    return s

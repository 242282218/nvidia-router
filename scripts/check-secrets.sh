#!/usr/bin/env bash

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

python3 - <<'PY'
import os
import posixpath
import re
import subprocess
import tempfile
import urllib.parse


SECRET_PATTERN = re.compile(rb"(?:nvapi-[A-Za-z0-9_-]{32,}|nvr_[A-Za-z0-9_-]{43})")
XK_PROXY_URL_PATTERN = re.compile(rb"https?://api[0-9]+\.xkdaili\.com/tools/XApi\.ashx\?[^\s\"'<>]+", re.IGNORECASE)
XK_PROXY_ENV_PATTERN = re.compile(rb"NVIDIA_ROUTER_XK_PROXY_API_URL[ \t]*[:=][ \t]*[\"']?([^\s\"']+)", re.IGNORECASE)
ALLOWLIST_PATH = "tests/e2e/keys.spec.ts"
ALLOWLIST_TOKEN = b"nvapi-fixture-not-a-real-key-" + b"123456789"
FORCED_CONTEXT_FILES = {".dockerignore", "Dockerfile"}


class ScanFailure(Exception):
    pass


def is_placeholder(value):
    normalized = value.strip().lower()
    if not normalized or normalized in {"invalid", "placeholder", "fixture", "test", "fake", "replace_me", "replace-with-real-value"}:
        return True
    if normalized.startswith("<") or normalized.endswith(">") or (normalized.startswith("${") and normalized.endswith("}")):
        return True
    return any(marker in normalized for marker in ("example.com", ".example.", ".test", ".invalid", "localhost", "127.0.0.1"))


def has_real_xk_credentials(raw_url):
    try:
        parsed = urllib.parse.urlsplit(raw_url.decode("utf-8"))
        values = urllib.parse.parse_qs(parsed.query, keep_blank_values=True)
    except (UnicodeDecodeError, ValueError):
        return False
    apikey = values.get("apikey", [""])[0]
    sign = values.get("sign", [""])[0]
    return bool(apikey and sign and not is_placeholder(apikey) and not is_placeholder(sign))


def inspect_xk_proxy_text(contents, relative_path):
    for match in XK_PROXY_URL_PATTERN.finditer(contents):
        if has_real_xk_credentials(match.group(0).rstrip(b".,);]")):
            raise ScanFailure("Potential Xingkong proxy credential found in tracked text.")
    for match in XK_PROXY_ENV_PATTERN.finditer(contents):
        value = match.group(1).rstrip(b".,);]")
        if not is_placeholder(value.decode("utf-8", errors="ignore")):
            raise ScanFailure("Potential Xingkong proxy URL found in tracked text.")


def scan_tracked_files(root):
    result = subprocess.run(
        ["git", "ls-files", "-z"], cwd=root, check=False, capture_output=True
    )
    if result.returncode != 0:
        raise ScanFailure("Failed to list tracked files for secret scanning.")
    for raw_path in result.stdout.split(b"\0"):
        if not raw_path:
            continue
        relative_path = raw_path.decode("utf-8", errors="strict").replace(os.sep, "/")
        absolute = os.path.join(root, *relative_path.split("/"))
        if os.path.islink(absolute):
            continue
        try:
            with open(absolute, "rb") as stream:
                inspect_xk_proxy_text(stream.read(), relative_path)
        except (OSError, UnicodeError) as error:
            raise ScanFailure("Failed to read a tracked file for secret scanning.") from error


class IgnorePattern:
    def __init__(self, raw):
        self.exclusion = raw.startswith("!")
        if self.exclusion:
            raw = raw[1:].strip()
        raw = posixpath.normpath(raw).lstrip("/")
        if not raw or raw == ".":
            raise ValueError("empty Docker ignore pattern")
        self.regex = re.compile(self._to_regex(raw))

    @staticmethod
    def _to_regex(pattern):
        result = ["^"]
        index = 0
        while index < len(pattern):
            char = pattern[index]
            if char == "*":
                if pattern[index:index + 2] == "**":
                    recursive_start = index
                    index += 2
                    has_separator = index < len(pattern) and pattern[index] == "/"
                    if has_separator:
                        index += 1
                    if index == len(pattern) or (recursive_start == 0 and not has_separator):
                        result.append(".*")
                    else:
                        result.append("(?:.*/)?")
                    continue
                result.append("[^/]*")
            elif char == "?":
                result.append("[^/]")
            elif char == "[":
                end = pattern.find("]", index + 1)
                if end == -1:
                    raise ValueError("invalid Docker ignore character class")
                character_class = pattern[index + 1:end]
                result.append("[" + character_class + "]")
                index = end
            elif char == "\\":
                index += 1
                if index == len(pattern):
                    result.append(r"\\")
                else:
                    result.append(re.escape(pattern[index]))
            else:
                result.append(re.escape(char))
            index += 1
        result.append("$")
        return "".join(result)

    def matches(self, relative_path):
        parts = relative_path.split("/")
        for end in range(len(parts), 0, -1):
            if self.regex.fullmatch("/".join(parts[:end])):
                return True
        return False


class DockerIgnore:
    def __init__(self, raw_patterns):
        self.patterns = []
        for raw in raw_patterns:
            if raw.startswith("#"):
                continue
            raw = raw.strip()
            if not raw:
                continue
            try:
                self.patterns.append(IgnorePattern(raw))
            except (ValueError, re.error) as error:
                raise ScanFailure("Failed to parse .dockerignore.") from error

    @property
    def has_exclusions(self):
        return any(pattern.exclusion for pattern in self.patterns)

    def ignored(self, relative_path):
        ignored = False
        for pattern in self.patterns:
            if not pattern.matches(relative_path):
                continue
            if pattern.exclusion:
                if ignored:
                    ignored = False
            elif not ignored:
                ignored = True
        return ignored


def load_dockerignore(root):
    path = os.path.join(root, ".dockerignore")
    try:
        with open(path, "r", encoding="utf-8-sig") as stream:
            return DockerIgnore(stream.readlines())
    except (OSError, UnicodeError) as error:
        raise ScanFailure("Failed to read .dockerignore.") from error


def included_in_context(relative_path, matcher):
    return relative_path in FORCED_CONTEXT_FILES or not matcher.ignored(relative_path)


def iter_context_files(root, matcher):
    def fail_walk(error):
        raise ScanFailure("Failed to traverse the Docker build context.") from error

    for current_root, directories, files in os.walk(
        root, topdown=True, onerror=fail_walk, followlinks=False
    ):
        relative_root = os.path.relpath(current_root, root).replace(os.sep, "/")
        if relative_root == ".":
            relative_root = ""
        kept_directories = []
        for directory in directories:
            absolute = os.path.join(current_root, directory)
            if directory == ".git" or os.path.islink(absolute):
                continue
            relative = "/".join(part for part in (relative_root, directory) if part)
            if not matcher.ignored(relative) or matcher.has_exclusions:
                kept_directories.append(directory)
        directories[:] = kept_directories

        for filename in files:
            if filename == ".git":
                continue
            absolute = os.path.join(current_root, filename)
            if os.path.islink(absolute):
                continue
            relative = "/".join(part for part in (relative_root, filename) if part)
            if included_in_context(relative, matcher):
                yield relative


def scan_file(root, relative_path, allowed):
    absolute = os.path.join(root, *relative_path.split("/"))
    overlap = 128
    carry = b""
    carry_previous = b""
    try:
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
        with os.fdopen(os.open(absolute, flags), "rb") as stream:
            while True:
                chunk = stream.read(1024 * 1024)
                if not chunk:
                    break
                contents = carry + chunk
                for match in SECRET_PATTERN.finditer(contents):
                    if match.end() <= len(carry):
                        continue
                    if match.start() == 0:
                        previous = carry_previous
                    else:
                        previous = contents[match.start() - 1:match.start()]
                    if previous and (
                        b"0" <= previous <= b"9"
                        or b"A" <= previous <= b"Z"
                        or b"a" <= previous <= b"z"
                        or previous == b"_"
                    ):
                        continue
                    if allowed.get(relative_path) == match.group(0):
                        continue
                    raise ScanFailure("Potential secret found in Docker build context.")
                if len(contents) > overlap:
                    carry_previous = contents[-overlap - 1:-overlap]
                else:
                    carry_previous = b""
                carry = contents[-overlap:]
    except OSError as error:
        raise ScanFailure("Failed to read a Docker build-context file.") from error


def scan_context(root, matcher, allowed):
    scanned = 0
    for relative_path in iter_context_files(root, matcher):
        scan_file(root, relative_path, allowed)
        scanned += 1
    if scanned == 0:
        raise ScanFailure("No Docker build-context files remained after .dockerignore filtering.")


def require(condition):
    if not condition:
        raise AssertionError("secret scan self-test failed")


def run_self_test():
    inspect_xk_proxy_text(
        b"NVIDIA_ROUTER_XK_PROXY_API_URL=https://proxy.example.test/?qty=1",
        "fixture.env",
    )
    try:
        inspect_xk_proxy_text(
            b"http://api2.xkdaili.com/tools/XApi.ashx?apikey=invalid&sign=invalid",
            "fixture.md",
        )
    except ScanFailure:
        raise AssertionError("placeholder Xingkong proxy URL was rejected")
    try:
        xk_path = b"2.xkdaili.com/tools/XApi.ashx"
        inspect_xk_proxy_text(
            b"http://api" + xk_path + b"?apikey=real-key&sign=real-sign",
            "fixture.md",
        )
    except ScanFailure:
        pass
    else:
        raise AssertionError("real Xingkong proxy URL was not detected")

    root_only = DockerIgnore(["/root-only"])
    require(root_only.ignored("root-only/value.txt"))
    require(not root_only.ignored("nested/root-only/value.txt"))

    directory = DockerIgnore(["cache/"])
    require(directory.ignored("cache/value.bin"))
    require(not directory.ignored("nested/cache/value.bin"))

    recursive = DockerIgnore(["**/generated/*.bin"])
    require(recursive.ignored("generated/value.bin"))
    require(recursive.ignored("nested/generated/value.bin"))

    embedded_recursive = DockerIgnore(["foo**bar"])
    require(not embedded_recursive.ignored("fooxbar"))
    require(embedded_recursive.ignored("foo/path/bar"))

    negated = DockerIgnore(["**/*.txt", "!keep.txt"])
    require(not negated.ignored("keep.txt"))
    require(negated.ignored("nested/keep.txt"))

    with tempfile.TemporaryDirectory() as fixture_root:
        with open(os.path.join(fixture_root, ".gitignore"), "w", encoding="utf-8") as stream:
            stream.write("ignored-context.txt\n")
        with open(os.path.join(fixture_root, "ignored-context.txt"), "wb") as stream:
            stream.write(b"nvapi-" + b"x" * 32 + b"\0binary")
        with open(os.path.join(fixture_root, "space name.txt"), "wb") as stream:
            stream.write(b"fixture data")
        os.mkdir(os.path.join(fixture_root, "ignored"))
        with open(os.path.join(fixture_root, "ignored", "keep.bin"), "wb") as stream:
            stream.write(b"fixture data")
        with open(os.path.join(fixture_root, "ignored", "drop.bin"), "wb") as stream:
            stream.write(b"fixture data")

        candidates = set(iter_context_files(fixture_root, DockerIgnore([])))
        require("ignored-context.txt" in candidates)
        require("space name.txt" in candidates)
        reinclude = set(iter_context_files(
            fixture_root, DockerIgnore(["ignored", "!ignored/keep.bin"])
        ))
        require("ignored/keep.bin" in reinclude)
        require("ignored/drop.bin" not in reinclude)
        try:
            scan_context(fixture_root, DockerIgnore([]), {})
        except ScanFailure:
            pass
        else:
            raise AssertionError("ignored build-context secret was not detected")

        target = os.path.join(fixture_root, "target.txt")
        link = os.path.join(fixture_root, "link.txt")
        try:
            with open(target, "wb") as stream:
                stream.write(b"nvapi-" + b"y" * 32)
            os.symlink(target, link)
        except (OSError, NotImplementedError):
            pass
        else:
            require("link.txt" not in set(iter_context_files(fixture_root, DockerIgnore([]))))

        boundary = os.path.join(fixture_root, "boundary.bin")
        with open(boundary, "wb") as stream:
            stream.write(
                b"z" * (1024 * 1024 - 129)
                + b"A"
                + b"nvapi-"
                + b"x" * 122
                + b"!"
            )
        scan_file(fixture_root, "boundary.bin", {})


if os.environ.get("NVIDIA_ROUTER_SECRET_SCAN_SELF_TEST") == "1":
    run_self_test()
    print("Secret scan self-test passed.")
    raise SystemExit(0)


try:
    root = os.getcwd()
    matcher = load_dockerignore(root)
    allowlist = {}
    allowlist_path = os.path.join(root, *ALLOWLIST_PATH.split("/"))
    if os.path.isfile(allowlist_path) and not os.path.islink(allowlist_path):
        allowlist[ALLOWLIST_PATH] = ALLOWLIST_TOKEN
    scan_context(root, matcher, allowlist)
    scan_tracked_files(root)
except ScanFailure as error:
    raise SystemExit(str(error))
PY

if [[ "${NVIDIA_ROUTER_SECRET_SCAN_SELF_TEST:-0}" == '1' ]]; then
  exit
fi
printf 'No NVIDIA or access key secrets found in Docker build context.\n'

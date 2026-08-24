"""dist 静态资源闭包检查（scripts/check-web-dist.sh 的等价实现）。

本机没有可用的 POSIX bash（见 memory.md §12），改用 Python 从
internal/web/dist/index.html 递归跟踪静态引用，确认：
  - 没有被引用却缺失的文件；
  - 没有上一次构建残留的孤立指纹产物（会被 go:embed 一起打进二进制）。
"""
import io
import os
import re
import sys

ROOT = os.path.join('internal', 'web', 'dist')
REFERENCE = re.compile(
    r'(?:href|src)="/([^"]+)"'
    r'|from\s*"/([^"]+)"'
    r'|import\(\s*"/([^"]+)"\s*\)'
    r'|"(assets/[^"]+)"'
)


def normalize(path):
    return path.replace(os.sep, '/')


def crawl():
    seen = set()
    queue = ['index.html']
    while queue:
        rel = queue.pop()
        if rel in seen:
            continue
        seen.add(rel)
        target = os.path.join(ROOT, rel)
        if not os.path.exists(target) or not rel.endswith(('.html', '.js', '.css')):
            continue
        text = io.open(target, encoding='utf-8', errors='ignore').read()
        for match in REFERENCE.finditer(text):
            found = next(group for group in match.groups() if group)
            if found.startswith(('http://', 'https://', 'data:')):
                continue
            queue.append(found.lstrip('/'))
    return seen


def on_disk():
    files = set()
    for dirpath, _, names in os.walk(ROOT):
        for name in names:
            files.add(normalize(os.path.relpath(os.path.join(dirpath, name), ROOT)))
    return files


def main():
    referenced = crawl()
    present = on_disk()
    missing = sorted(referenced - present)
    orphans = sorted(present - referenced)
    print('referenced: %d   on disk: %d' % (len(referenced), len(present)))
    print('missing   : %s' % (missing or 'none'))
    print('orphans   : %s' % (orphans or 'none'))
    return 1 if missing or orphans else 0


if __name__ == '__main__':
    sys.exit(main())

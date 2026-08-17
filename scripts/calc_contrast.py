#!/usr/bin/env python3
"""WCAG 2.x contrast ratio calculator for the web theme tokens.

Usage:
    python scripts/calc_contrast.py            # print all registered pairs
    python scripts/calc_contrast.py "#18181b" "#ffffff"  # ad-hoc pair

Formula: WCAG relative luminance (sRGB, threshold 0.03928) then
(L_lighter + 0.05) / (L_darker + 0.05). Kept in the repo because the
design-aesthetics knowledge base forbids using any text/background pair
whose ratio has not been actually computed and registered.
Theme: Minimal Product (Light) from design-aesthetics knowledge base.
"""

from __future__ import annotations

import sys

# Raw values from web/src/styles/theme.css (Minimal Product / Light). Keep in sync.
CANVAS = "#ffffff"
SURFACE = "#fafafa"
ELEVATED = "#ffffff"
SUNKEN = "#fafafa"

TEXT = "#09090b"
TEXT_SECONDARY = "#3f3f46"
TEXT_MUTED = "#52525b"
TEXT_SUBTLE = "#71717a"

ACCENT = "#18181b"
ACCENT_BRIGHT = "#18181b"
ACCENT_FOREGROUND = "#ffffff"

SUCCESS = "#146c2e"
SUCCESS_BACKGROUND = "#e8f0ea"  # 10% tint on #ffffff
SUCCESS_FOREGROUND = "#146c2e"
WARNING = "#7a4e00"
WARNING_BACKGROUND = "#f2ede6"  # 10% tint on #ffffff
WARNING_FOREGROUND = "#7a4e00"
DANGER = "#b3261e"
DANGER_BACKGROUND = "#f7e9e8"  # 10% tint on #ffffff
DANGER_FOREGROUND = "#b3261e"
INFO = "#3d5aa0"
INFO_BACKGROUND = "#eceef6"  # 10% tint on #ffffff
INFO_FOREGROUND = "#3d5aa0"

FOCUS = "#18181b"
MUTED_BACKGROUND = "#f4f4f5"
MUTED_FOREGROUND = "#52525b"
DISABLED_BACKGROUND = "#f4f4f5"
DISABLED_FOREGROUND = "#71717a"

BRAND = "#76b900"
BRAND_FOREGROUND = "#081000"

BORDER_STRONG = "#8b8b93"

# name, foreground, background, required ratio
PAIRS: list[tuple[str, str, str, float]] = [
    # body text roles, 4.5:1
    ("text on canvas", TEXT, CANVAS, 4.5),
    ("text on surface", TEXT, SURFACE, 4.5),
    ("text on elevated", TEXT, ELEVATED, 4.5),
    ("text-secondary on canvas", TEXT_SECONDARY, CANVAS, 4.5),
    ("text-secondary on surface", TEXT_SECONDARY, SURFACE, 4.5),
    ("text-secondary on elevated", TEXT_SECONDARY, ELEVATED, 4.5),
    ("text-muted on canvas", TEXT_MUTED, CANVAS, 4.5),
    ("text-muted on surface", TEXT_MUTED, SURFACE, 4.5),
    ("text-muted on elevated", TEXT_MUTED, ELEVATED, 4.5),
    ("text-subtle on surface", TEXT_SUBTLE, SURFACE, 4.5),
    ("text-subtle on elevated", TEXT_SUBTLE, ELEVATED, 4.5),
    # accent roles, 4.5:1 for text usage
    ("accent-text on canvas", ACCENT, CANVAS, 4.5),
    ("accent-text on surface", ACCENT, SURFACE, 4.5),
    ("accent-text on elevated", ACCENT, ELEVATED, 4.5),
    ("accent-foreground on accent", ACCENT_FOREGROUND, ACCENT, 4.5),
    # status text on page surfaces, 4.5:1
    ("success-text on canvas", SUCCESS, CANVAS, 4.5),
    ("success-text on surface", SUCCESS, SURFACE, 4.5),
    ("success-text on elevated", SUCCESS, ELEVATED, 4.5),
    ("warning-text on canvas", WARNING, CANVAS, 4.5),
    ("warning-text on surface", WARNING, SURFACE, 4.5),
    ("warning-text on elevated", WARNING, ELEVATED, 4.5),
    ("danger-text on canvas", DANGER, CANVAS, 4.5),
    ("danger-text on surface", DANGER, SURFACE, 4.5),
    ("danger-text on elevated", DANGER, ELEVATED, 4.5),
    ("info-text on canvas", INFO, CANVAS, 4.5),
    ("info-text on surface", INFO, SURFACE, 4.5),
    ("info-text on elevated", INFO, ELEVATED, 4.5),
    # tint status chips: foreground on tinted background, 4.5:1
    ("success-foreground on success-background", SUCCESS_FOREGROUND, SUCCESS_BACKGROUND, 4.5),
    ("warning-foreground on warning-background", WARNING_FOREGROUND, WARNING_BACKGROUND, 4.5),
    ("danger-foreground on danger-background", DANGER_FOREGROUND, DANGER_BACKGROUND, 4.5),
    ("info-foreground on info-background", INFO_FOREGROUND, INFO_BACKGROUND, 4.5),
    ("muted-foreground on muted-background", MUTED_FOREGROUND, MUTED_BACKGROUND, 4.5),
    ("disabled-foreground on disabled-background", DISABLED_FOREGROUND, DISABLED_BACKGROUND, 4.0),
    # brand logo badge, 4.5:1
    ("brand-foreground on brand", BRAND_FOREGROUND, BRAND, 4.5),
    # non-text UI: control borders and focus ring, 3:1
    ("focus ring on canvas", FOCUS, CANVAS, 3.0),
    ("focus ring on surface", FOCUS, SURFACE, 3.0),
    ("focus ring on elevated", FOCUS, ELEVATED, 3.0),
    ("border-strong on canvas", BORDER_STRONG, CANVAS, 3.0),
    ("border-strong on surface", BORDER_STRONG, SURFACE, 3.0),
    # chart data colors against chart backgrounds, 3:1 (non-text)
    ("chart danger on canvas", DANGER, CANVAS, 3.0),
    ("chart warning on canvas", WARNING, CANVAS, 3.0),
    ("chart success on canvas", SUCCESS, CANVAS, 3.0),
    ("chart info on canvas", INFO, CANVAS, 3.0),
]


def _channel(value: float) -> float:
    return value / 12.92 if value <= 0.03928 else ((value + 0.055) / 1.055) ** 2.4


def relative_luminance(hex_color: str) -> float:
    hex_color = hex_color.lstrip("#")
    if len(hex_color) == 3:
        hex_color = "".join(ch * 2 for ch in hex_color)
    r, g, b = (_channel(int(hex_color[i : i + 2], 16) / 255) for i in (0, 2, 4))
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast_ratio(fg: str, bg: str) -> float:
    l1 = relative_luminance(fg)
    l2 = relative_luminance(bg)
    lighter, darker = max(l1, l2), min(l1, l2)
    return (lighter + 0.05) / (darker + 0.05)


def main(argv: list[str]) -> int:
    if len(argv) == 3:
        print(f"{contrast_ratio(argv[1], argv[2]):.2f}:1")
        return 0
    failures = 0
    print(f"{'pair':<48}{'ratio':>10}{'required':>10}  verdict")
    for name, fg, bg, required in PAIRS:
        ratio = contrast_ratio(fg, bg)
        ok = ratio >= required
        failures += 0 if ok else 1
        mark = "PASS" if ok else "FAIL"
        print(f"{name:<48}{ratio:>9.2f}:{required:>9.2f}  {mark}")
    print(f"\n{len(PAIRS) - failures}/{len(PAIRS)} pairs pass")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))

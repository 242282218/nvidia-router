#!/usr/bin/env python3
"""WCAG 2.x contrast ratio calculator for the web theme tokens.

Usage:
    python scripts/calc_contrast.py            # print all registered pairs
    python scripts/calc_contrast.py "#76b900" "#0b0d0f"  # ad-hoc pair

Formula: WCAG relative luminance (sRGB, threshold 0.03928) then
(L_lighter + 0.05) / (L_darker + 0.05). Kept in the repo because the
design-aesthetics knowledge base forbids using any text/background pair
whose ratio has not been actually computed and registered.
"""

from __future__ import annotations

import sys

# Raw values from web/src/styles/theme.css. Keep in sync when tokens change.
CANVAS = "#0b0d0f"
SURFACE = "#111416"
ELEVATED = "#171b1f"
SUNKEN = "#07090b"

TEXT = "#edf1f2"
TEXT_SECONDARY = "#a7b0b8"
TEXT_MUTED = "#7f8a94"
TEXT_SUBTLE = "#7d8994"

ACCENT = "#76b900"
ACCENT_BRIGHT = "#8bd31f"
ACCENT_FOREGROUND = "#081000"

SUCCESS = "#4ade80"
SUCCESS_BACKGROUND = "#6dd58c"
SUCCESS_FOREGROUND = "#07140b"
WARNING = "#fbbf24"
WARNING_BACKGROUND = "#e0b354"
WARNING_FOREGROUND = "#201500"
DANGER = "#f87171"
DANGER_BACKGROUND = "#f2857c"
DANGER_FOREGROUND = "#240608"
INFO = "#7c9cff"
INFO_BACKGROUND = "#7c9cff"
INFO_FOREGROUND = "#0d1122"

FOCUS = "#8ea6ff"
MUTED_BACKGROUND = "#2a3036"
MUTED_FOREGROUND = "#edf1f2"
DISABLED_BACKGROUND = "#1d2328"
DISABLED_FOREGROUND = "#a7b0b8"

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
    ("accent-text (bright) on canvas", ACCENT_BRIGHT, CANVAS, 4.5),
    ("accent-text (bright) on surface", ACCENT_BRIGHT, SURFACE, 4.5),
    ("accent-text (bright) on elevated", ACCENT_BRIGHT, ELEVATED, 4.5),
    ("accent-foreground on accent", ACCENT_FOREGROUND, ACCENT, 4.5),
    ("accent-foreground on accent-bright", ACCENT_FOREGROUND, ACCENT_BRIGHT, 4.5),
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
    # solid status chips: foreground on tinted background, 4.5:1
    ("success-foreground on success-background", SUCCESS_FOREGROUND, SUCCESS_BACKGROUND, 4.5),
    ("warning-foreground on warning-background", WARNING_FOREGROUND, WARNING_BACKGROUND, 4.5),
    ("danger-foreground on danger-background", DANGER_FOREGROUND, DANGER_BACKGROUND, 4.5),
    ("info-foreground on info-background", INFO_FOREGROUND, INFO_BACKGROUND, 4.5),
    ("muted-foreground on muted-background", MUTED_FOREGROUND, MUTED_BACKGROUND, 4.5),
    ("disabled-foreground on disabled-background", DISABLED_FOREGROUND, DISABLED_BACKGROUND, 4.5),
    # non-text UI: control borders and focus ring, 3:1
    ("focus ring on canvas", FOCUS, CANVAS, 3.0),
    ("focus ring on surface", FOCUS, SURFACE, 3.0),
    ("focus ring on elevated", FOCUS, ELEVATED, 3.0),
    ("accent as icon/dot on canvas", ACCENT, CANVAS, 3.0),
    ("accent as icon/dot on surface", ACCENT, SURFACE, 3.0),
    # chart data colors against chart backgrounds, 3:1 (non-text)
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

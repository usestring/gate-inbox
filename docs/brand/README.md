<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Brand

The wordmark is a shell prompt: an amber `❯`, then `gate/inbox` in JetBrains Mono ExtraBold, with
the slash in the same amber. The mark is the prompt alone on a warm near-black square.

Every glyph is outlined rather than live text, so these render identically wherever the font is
not installed. No font file is in the repository.

| File | Use |
|---|---|
| `wordmark-light.svg` | Light backgrounds |
| `wordmark-dark.svg` | Dark backgrounds, including the README in dark mode |
| `mark.svg` | Square contexts: avatars, social cards |
| `mark-512.png` | Raster, for anything that will not take an SVG |

## Colours

| Role | Light | Dark |
|---|---|---|
| Ink (`gate`, `inbox`) | `#1f1a14` | `#f3ead8` |
| Accent (`❯`, `/`) | `#b45309` | `#f59e0b` |
| Mark background | `#17130c` | |

The mark always uses the dark accent. Each accent passes contrast only on its own background:
`#b45309` is 5.0:1 on white and `#f59e0b` is 8.8:1 on `#0d1117`, but neither works on the other.

## Geometry

The two wordmarks share one geometry; only the colours differ. Every measure is taken from the
font's own metrics in JetBrains Mono ExtraBold (1000 units per em):

- The `❯` is the font's own prompt glyph, keeping its arm slope and its 168-unit horizontal stroke,
  but shortened to run from the baseline to the flat x-height (550, the top of `x`). Its arm ends
  are flat, so it takes no overshoot.
- The ink gap between `❯` and `g` is one space advance (600 units).
- The slash is the font's own `/`, keeping its 155-unit stroke and 0.367 slant, lengthened along
  its own axis to run from the descender of `g` (−180) to the ascender of `b` (730). It keeps its
  600-unit cell, so the spacing on either side is the font's.
- In `mark.svg` the prompt is 45% of the square's height, with its area centroid on the centre.

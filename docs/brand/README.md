# Brand

| File | Use |
| --- | --- |
| `wordmark-light.svg` | The name, on light backgrounds. The app itself shows only the wordmark. |
| `wordmark-dark.svg` | The name, on dark backgrounds. |
| `icon.svg` | The app icon, the source of `resources/icon.png`. |
| `tray-wordmark.svg` | The tray wordmark, optically centered for the menu bar. |
| `tray-o-mark.svg` | The standalone O mark, without the `me!r` lettering. |
| `resources/tray-wordmark.png` | The wordmark export used by the macOS and Linux tray. |
| `resources/tray-o-mark.png` | The O mark export used by the macOS and Linux tray. |

The wordmark reads **me!ro**. The *i* is MyGO!!!!!'s `!`, tilted like a lost
compass needle, and the *o* is a ring of five overlapping pieces, one per
member, around a play mark. Each piece lies on the one before it; a thin cut
separates them. The cuts are transparent, so the wordmarks sit on any
background. The letters are outlined from Google Sans Bold, so no font is
needed to show them.

## Colours

| Name | Hex | Where |
| --- | --- | --- |
| MyGO!!!!! | `#3388BB` | The icon's background, from `#469DD3` at the top to `#1F5C8C` at the bottom |
| Tomori | `#77BBDD` | Ring |
| Anon | `#FF8899` | Ring, and the dot of the `!` |
| Rāna | `#77DD77` | Ring |
| Soyo | `#FFDD88` | Ring, on dark backgrounds |
| Soyo, deeper | `#FFBB12` | Ring, on light backgrounds, where `#FFDD88` is too pale |
| Taki | `#7777AA` | Ring |
| Ink | `#1D1B20` | Letters on light backgrounds; `#F3F1F8` on dark ones |

The member and band colours are MyGO!!!!!'s own image colours.

## The icon

`icon.svg` is flat shapes with light Icon Composer-style shading: a gradient
background with a lit rim, a gentle top-to-bottom shade and a hairline
highlight on each piece, and one soft shadow under the ring and the play mark.

`resources/icon.png` is `icon.svg` exported at 1024 × 1024 with a transparent
background, the rounded square scaled to 824 px and centred (the macOS icon
grid), with a soft shadow under it.

The macOS and Linux tray images are transparent PNG exports of the two tray
SVGs: `tray-wordmark.svg` at 191 × 64 px, and `tray-o-mark.svg` at 64 × 64 px.
The wordmark export slightly shortens the `!` stem and reframes the artwork so
each coordinate of its alpha-weighted centre is within 0.24 px of the canvas
centre. The O export contains only the five-piece ring and play mark, with
extra transparent space around it for a quieter appearance. Windows uses
`icon.png` instead: its
notification area reuses the tray icon in track notifications, and template
icons become a solid white square on a dark taskbar.

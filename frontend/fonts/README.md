# Bundled interface fonts

These assets are bundled so the shell does not depend on a user's operating
system fonts or an external font CDN at runtime.

| Family | Source | License |
| --- | --- | --- |
| Inter | [Google Fonts](https://fonts.google.com/specimen/Inter) | [OFL-1.1](https://scripts.sil.org/OFL) |
| Atkinson Hyperlegible Next | [Google Fonts](https://fonts.google.com/specimen/Atkinson+Hyperlegible+Next) | [OFL-1.1](https://scripts.sil.org/OFL) |
| Source Sans 3 | [Google Fonts](https://fonts.google.com/specimen/Source+Sans+3) | [OFL-1.1](https://scripts.sil.org/OFL) |
| Noto Sans Symbols 2 | [Noto Fonts](https://fonts.google.com/noto/specimen/Noto+Sans+Symbols+2) | [OFL-1.1](https://scripts.sil.org/OFL) |
| Noto Color Emoji | [Noto Emoji](https://github.com/googlefonts/noto-emoji) | [OFL-1.1](https://scripts.sil.org/OFL) |

The UI options are intentionally limited to four families. Noto Sans Symbols 2
and the vector COLRv1 build of Noto Color Emoji are coverage fallbacks, not
selectable body faces. Browsers without COLRv1 support continue to the
monochrome symbol or browser fallback.

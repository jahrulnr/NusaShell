# CodeMirror 5 vendor bundle

CodeMirror 5 is vendored here (no build step) and lazy-loaded from
`/vendor/codemirror/...` by `frontend/js/code-language.js` for the local
file text/code preview in `frontend/js/media-zoom.js`.

## Version

CodeMirror 5.65.18 (MIT license — see <https://spdx.org/licenses/MIT>).

## Upstream source URLs (cdnjs / Cloudflare)

All files were downloaded from
`https://cdnjs.cloudflare.com/ajax/libs/codemirror/5.65.18/...`

- `codemirror.min.js`        → `codemirror.min.js`
- `codemirror.min.css`       → `codemirror.min.css`
- `theme/dracula.min.css`    → `theme/dracula.min.css`
- `mode/javascript.min.js`   → `mode/javascript/javascript.min.js`
- `mode/xml.min.js`          → `mode/xml/xml.min.js`
- `mode/htmlmixed.min.js`    → `mode/htmlmixed/htmlmixed.min.js`
- `mode/css.min.js`          → `mode/css/css.min.js`
- `mode/markdown.min.js`     → `mode/markdown/markdown.min.js`
- `mode/shell.min.js`        → `mode/shell/shell.min.js`
- `mode/python.min.js`       → `mode/python/python.min.js`
- `mode/go.min.js`           → `mode/go/go.min.js`
- `mode/yaml.min.js`         → `mode/yaml/yaml.min.js`
- `mode/sql.min.js`          → `mode/sql/sql.min.js`

Upstream project: <https://github.com/codemirror/CodeMirror> (5.x branch).

## sha256 + size of each downloaded file

| File                     | Size (bytes) | sha256                                                          |
| ------------------------ | -----------: | --------------------------------------------------------------- |
| codemirror.min.js        |     170,535  | 5df4d971e24aea483bf8ed5b48e026f3806792436ef29b1248aef7c754d161f9 |
| codemirror.min.css       |       6,037  | 11077112ab6955d29fe41085c62365c7d4a2f00a570c7475e2aec2a8cbc85fc4 |
| theme/dracula.min.css    |       1,646  | ba8d009adc9d54938ea88252c099a2b773ed3a4f5515ae9c2f937a8a4cb399df |
| mode/javascript.min.js   |      17,315  | 99b46f351b4b1ce8a14cdf04fe4235ecb429b5b7b986867034a7dc195a710a58 |
| mode/xml.min.js          |       5,981  | 1403f6fc04264c38e933891710636ed07761898aa764de95a39a832d433cbe66 |
| mode/htmlmixed.min.js    |       2,870  | 506e12c1fc68437c2f5d45b2d7c8cd341f231ad94c5126667c0272bfe6359e6a |
| mode/css.min.js          |      27,098  | ee0ce00f346a6d7e345a74ab32f2cc600e8b7df878b28715d7d52192fa274eee |
| mode/markdown.min.js     |      14,631  | 6d31310a4719d151d198b604864fa7cb7dcaa5013888863585e06d7c7085f3d8 |
| mode/shell.min.js        |       2,753  | 08aed7c67ef9d68f8f99eec9af2da275da760b3296615f341f44a5ebae3fbbd7 |
| mode/python.min.js       |       6,500  | 6d19a4ba8b05a354935ceebf490582faffa047c86c4715a2b504b14319eb6399 |
| mode/go.min.js           |       3,043  | c786600d4e4094ead1290122975c88dc6ca54a973bcc09dd2104fdafa9d2c1d9 |
| mode/yaml.min.js         |       1,819  | a6925495d3ccd0197fbc85df616cae466f3ba7a04b885db3c4e7d6952a859623 |
| mode/sql.min.js          |      47,294  | 8b2eb6ce3ada53b11cef6e80afb0b290de6a57a3722c08f8efb3d1dc06a80919 |

## License

CodeMirror 5 is MIT licensed. The full license text is available at
<https://spdx.org/licenses/MIT> and in the upstream repository
(`LICENSE` at the CodeMirror 5.x branch root).

## Notes

- These files are vendored static assets; there is no build step and no
  npm dependency at runtime. They are embedded with the rest of `frontend/`
  by `frontend/embed.go` and served at `/vendor/codemirror/...`.
- CodeMirror 5 has no native TypeScript mode; `text/typescript` reuses the
  JavaScript mode (`mode/javascript.min.js`) while keeping the TypeScript
  display label.
- The Dracula theme (`theme/dracula.min.css`) is the only theme shipped;
  the local-file preview editor is read-only and uses this theme.

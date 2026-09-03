# V2 Pet Contract

## Sprite Atlas

- Version: `spriteVersionNumber: 2` (mandatory; without it the runtime
  defaults to the 9-row v1 contract and rejects the 2288-pixel-tall asset).
- Format: PNG or WebP (NusaShell runtime decodes WebP; package WebP).
- Dimensions: `1536x2288`, 8 columns x 11 rows, `192x208` cells.
- Background: transparent. Fully transparent pixels carry zero RGB residue.
- Rows 0-8: standard animation states. Rows 9-10: 16 clockwise look
  directions. Unused standard-row cells (after each row's last used column)
  must be fully transparent; all look-row cells are used.
- The 8x9 `1536x1872` atlas is an intermediate assembly artifact only. Never
  package it as a newly hatched pet.

## Rows

| Row | State             | Used columns | Durations (ms, upstream Codex app)     |
| --- | ----------------- | -----------: | -------------------------------------- |
| 0   | idle              |          0-5 | 280, 110, 110, 140, 140, 320           |
| 1   | running-right     |          0-7 | 120 each, final 220                    |
| 2   | running-left      |          0-7 | 120 each, final 220                    |
| 3   | waving            |          0-3 | 140 each, final 280                    |
| 4   | jumping           |          0-4 | 140 each, final 280                    |
| 5   | failed            |          0-7 | 140 each, final 240                    |
| 6   | waiting           |          0-5 | 150 each, final 260                    |
| 7   | running           |          0-5 | 120 each, final 220                    |
| 8   | review            |          0-5 | 150 each, final 280                    |
| 9   | look directions A |          0-7 | 000, 022.5, 045, 067.5, 090, 112.5, 135, 157.5 |
| 10  | look directions B |          0-7 | 180, 202.5, 225, 247.5, 270, 292.5, 315, 337.5 |

The atlas is a still image; playback timing is owned by the consuming app.
The NusaShell runtime (`apps/pets/internal/char/atlas.go`,
`DefaultAtlasLayout`) applies its own delays to the rows it maps: `idle` 0,
`thinking` 7 (running), `reasoning` 8 (review), `done` 3 (waving, play once),
`error` 5 (failed, play once), `waiting` 6, plus look rows 9-10 for idle hover
gaze. The frame counts above are still mandatory for a valid atlas.

## Row purposes

- `idle`: calm breathing/blinking loop; first frame doubles as a reduced-motion still.
- `running-right` / `running-left`: directional locomotion with alternating cadence; mirror `running-left` only when identity and prop handedness survive.
- `waving`: greeting with clear start, raised gesture, return.
- `jumping`: anticipation, lift, peak, descent, settle.
- `failed`: readable error/sad reaction without noisy detached effects.
- `waiting`: expectant asking pose for approval or input.
- `running`: active task work/processing, never literal foot-running.
- `review`: focused inspection of completed output.
- rows 9-10: one continuous clockwise 16-pose look loop.

`000` degrees means looking up / 12 o'clock, not neutral/front. Neutral/front
is the pointer deadzone and falls back to the idle animation.

## Package

```text
<pet-id>/
├── pet.json
└── spritesheet.webp
```

```json
{
  "id": "pet-name",
  "displayName": "Pet Name",
  "description": "One short sentence.",
  "spriteVersionNumber": 2,
  "spritesheetPath": "spritesheet.webp"
}
```

Stage the package to `<datadir>/pets/<pet-id>/`, where `<datadir>` is the
NusaShell data directory (default `~/.config/nusashell`; the runtime context
reports the active path). Point a local `apps/pets` run at the spritesheet via
its config `spritesheet` key once that runtime phase is wired; keep the run
folder in the workspace for QA artifacts.

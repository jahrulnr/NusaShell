# Shell workbench visual contract

NusaShell presents local AI tools as an instrument workbench: a persistent
graphite frame contains navigation, status, and the active workspace. The shell
should feel closer to dependable technical equipment than a web dashboard. Its
visual hierarchy comes from nested borders, restrained surface changes, and
compact utility typography rather than stacked cards or large shadows.

## Visual system

- **Void** `#0d0f0e`, **panel** `#131514`, and **raised panel** `#202320` form
  the graphite surface hierarchy. Borders become lighter as a control becomes
  more interactive or physically closer to the user.
- **Phosphor** `#c5f45d` is reserved for selected navigation, connection and
  running state, keyboard focus, and primary actions. Large decorative lime
  fields and gradients are outside the shell language. Selection borders use a
  translucent phosphor tint; the full-strength color is reserved for focus,
  compact indicators, and filled actions.
- Space Grotesk carries product and page titles; IBM Plex Sans carries prose;
  IBM Plex Mono carries navigation, status, search, paths, logs, and compact
  operational metadata. System fallbacks must remain usable offline.
- Corners are compact, normally 5–10 pixels. The outer frame and plugin launch
  plates may use inset rails and fastener details; ordinary cards must not copy
  that decoration.

## Shell layout

```text
┌─ brand · connection ─────────────────── settings · pin · window controls ─┐
├──────────────────────┬─────────────────────────────────────────────────────┤
│ Home                 │                                                     │
│ Agent                │  active workspace                                  │
│ Skills               │                                                     │
│ Learning             │  Home: title → scoped search → launch plates        │
│ Plugins              │  Workbench: fixed header → bounded working panes    │
│ AI Providers         │                                                     │
│ Autostart            │                                                     │
│ Logs / Jobs          │                                                     │
│                      │                                                     │
│ Add Plugin           │                                                     │
│ ──────────────────── │                                                     │
│ Docs / Collapse      │                                                     │
└──────────────────────┴─────────────────────────────────────────────────────┘
```

At wide widths the labelled sidebar is deliberately stable and generous enough
for operational labels. At the 900-pixel Electron minimum it collapses to icons
so the active workspace retains useful width. At narrow browser-preview widths
the sidebar hides; no workspace action may depend exclusively on its expanded
state.

## Home launcher

Home is an app launcher, not an analytics overview. It opens with product
identity and a single scoped search followed by windowed plugin launch plates.
Each plate gives mixed plugin artwork equal visual weight, exposes a readable
name, and includes a textual runtime state when applicable. Hover raises the
plate by two pixels; selection and running state remain understandable without
animation or color alone. Plugin artwork keeps its source colors: themed glyphs
must be supplied as assets rather than produced with CSS grayscale or tint
filters.

## Interaction rules

- Every actionable surface uses a native interactive element and has a visible
  phosphor focus ring.
- Motion is brief and physical (small lift or press), never ambient. Reduced
  motion disables the transitions.
- Empty and error states explain what happened and the next available action.
- Destructive actions keep the shell's red semantic color and require the
  existing confirmations. Phosphor never represents failure.
- Scroll belongs to the active data surface: message thread, file tree, editor,
  log tail, or modal body. Shell chrome remains stable.

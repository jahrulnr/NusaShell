# Skills workspace visual contract

The Skills view extends NusaShell's dark workbench language with a compact,
three-pane artifact browser. It is a management surface, not a marketplace:
the interface prioritizes package identity, actual file structure, and safe
local editing over promotional cards.

At desktop widths, the managed skill library, package tree, and file viewer
share one bordered workspace. Selecting a skill opens `SKILL.md` first. Folder
depth is visible in the tree; editable UTF-8 files and read-only binary or large
files use different glyphs and explicit metadata. The editor owns the remaining
space and uses the shell's utility monospace face. Unsaved changes require
confirmation before another file or skill replaces them.

Install package opens Electron's native picker for `.skill` and `.zip` files.
Deletion targets only NusaShell's managed copy and requires confirmation.
Empty, unavailable-bridge, installation-error, and read-error states must state
what happened and what action is available. At narrow preview widths the panes
stack in library, files, editor order without hiding any management action.

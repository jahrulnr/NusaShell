# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Focused agent modules.** The agent view now separates transcript rendering,
  composer interaction, and model selection while keeping the existing browser
  API and UI behavior intact.
- **Cross-platform workspace picker.** Folder selection now uses the native
  system dialog on Windows and macOS as well as supported Linux desktop
  dialogs.

### Fixed

- **Static JavaScript MIME compatibility.** The asset check accepts either
  standard JavaScript media type returned by the operating system, preventing
  a Windows-only CI failure.
- **Resilient upstream recovery.** Transient provider failures now retry with
  bounded backoff and `Retry-After` support; interrupted streaming output is
  preserved and continued once without repeating completed tool work.

## [0.1.0] - 2026-08-13

### Added

- **NusaShell Light Go port.** A self-contained Go application serves an
  embedded native-JavaScript frontend with multi-conversation agent chat,
  Messages, Responses, and Chat provider formats, MCP, skills, memory,
  document search, and local credential storage.
- **Workspace, context, and attachments in the agent composer.** Each active
  conversation can select a folder through the native picker, display context
  usage, and attach files to a turn.
- **Electron-aligned execution timeline.** Reasoning renders Markdown, while
  reasoning and tool calls remain collapsed by default. Long tool output scrolls
  within its panel after ten lines.
- **GitHub Actions verification.** Pull requests and pushes to `master` run
  frontend tests plus Go formatting, vet, race-test, and build gates on the
  supported operating systems.

### Changed

- **Conversation UI parity.** The conversation rail, composer, workspace
  control, context counter, attachment control, and tool presentation now
  follow the NusaShell Electron interaction model.

### Fixed

- **Timeline alignment and readability.** Reasoning entries line up with tool
  entries, and Markdown content in reasoning is rendered as formatted HTML
  instead of raw source text.
- **Windows CI formatting check.** Source checkouts now preserve LF line
  endings, so `gofmt` evaluates the repository content consistently on every
  supported operating system.

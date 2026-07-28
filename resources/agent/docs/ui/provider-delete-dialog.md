# Delete Provider Dialog

Confirmation before removing a provider and its saved credentials.

**How to open:** Click Delete on an AI Providers card or on the Provider Details view.

## Confirmation

Warns that the saved credential, imported models, and connection settings will be removed from this device.

- **Delete overlay** (`#provider-delete-overlay`):
  - Section: Confirmation
  - Type: overlay
  - Action: Clicking outside the delete provider dialog closes it.

- **Delete provider confirmation** (`#provider-delete-dialog`):
  - Section: Confirmation
  - Type: dialog
  - Action: Confirms removal of the provider and its saved credentials.

- **Dialog title** (`#provider-delete-title`):
  - Section: Confirmation
  - Type: heading
  - Action: Shows 'Delete {provider name}?' or 'Delete provider?'.

- **Dialog description** (`#provider-delete-copy`):
  - Section: Confirmation
  - Type: text
  - Action: Explains that the credential, imported models, and settings will be removed.

- **Close dialog** (`#provider-delete-close`):
  - Section: Confirmation
  - Type: icon button
  - Action: Closes the delete provider dialog.

- **Cancel** (`#provider-delete-cancel`):
  - Section: Confirmation
  - Type: button
  - Action: Closes the dialog without deleting.

- **Delete** (`#provider-delete-confirm`):
  - Section: Confirmation
  - Type: button
  - Action: Permanently deletes the provider after disabling the button and showing 'Deleting…'.

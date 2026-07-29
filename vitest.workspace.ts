import { defineWorkspace } from "vitest/config";

export default defineWorkspace([
  "packages/domain",
  "packages/application",
  "packages/infrastructure",
  "packages/contracts",
  "packages/transport-ws",
  "packages/plugin-sdk",
  "apps/backend",
  "apps/desktop",
  "plugins/examples/mail",
  "scripts",
]);

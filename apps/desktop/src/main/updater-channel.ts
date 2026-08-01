import { join, normalize, posix, resolve, sep, win32 } from "node:path";

export type UpdateChannel = "dir-install" | "appimage" | "system-package" | "unmanaged";

export interface UpdateChannelInput {
  readonly platform: NodeJS.Platform;
  readonly exePath: string;
  readonly appImage: string | undefined;
  readonly homeDir: string;
  readonly localAppData?: string;
}

/** Identify only the install locations that NusaShell itself owns. */
export function classifyUpdateChannel(input: UpdateChannelInput): UpdateChannel {
  if (input.appImage) return "appimage";

  const pathApi = input.platform === "win32" ? win32 : { join, normalize, resolve, sep };
  const executable = pathApi.normalize(input.exePath);
  if (input.platform === "linux") {
    if (isWithin(executable, pathApi.join(input.homeDir, ".local", "share", "nusashell", "versions"), pathApi)) {
      return "dir-install";
    }
    if (isWithin(executable, "/opt", pathApi) || isWithin(executable, "/usr", pathApi)) return "system-package";
  }
  if (input.platform === "darwin" && isWithin(executable, pathApi.join(input.homeDir, "Applications", "NusaShell.app"), pathApi)) {
    return "dir-install";
  }
  if (input.platform === "win32") {
    const localAppData = input.localAppData ?? pathApi.join(input.homeDir, "AppData", "Local");
    if (isWithin(executable, pathApi.join(localAppData, "Programs", "NusaShell", "versions"), pathApi)) {
      return "dir-install";
    }
  }
  return "unmanaged";
}

function isWithin(candidate: string, parent: string, pathApi: Pick<typeof posix, "resolve" | "sep">): boolean {
  const resolvedCandidate = pathApi.resolve(candidate);
  const resolvedParent = pathApi.resolve(parent);
  return resolvedCandidate === resolvedParent || resolvedCandidate.startsWith(`${resolvedParent}${pathApi.sep}`);
}

export const PROTOCOL_VERSION = "1.0";

export const SUPPORTED_VERSIONS: readonly string[] = ["1.0"];

export function isSupportedVersion(version: string): boolean {
  return SUPPORTED_VERSIONS.includes(version);
}

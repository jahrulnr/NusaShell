export interface RuntimeOsProbe {
  readonly platform?: string;
  readonly fileExists?: (path: string) => boolean;
  readonly readTextFile?: (path: string) => string | undefined;
}

/**
 * Detect the host OS/runtime for agent prompt context.
 *
 * Values look like: `docker`, `docker (ubuntu)`, `linux (debian)`,
 * `windows`, `macos`, or `linux` / the raw Node platform as fallback.
 *
 * When no probe is supplied (or the probe omits `platform`), returns
 * `"unknown"`. Real OS detection is provided by an infrastructure adapter
 * implementing {@link RuntimeOsProbe} (e.g. `NodeRuntimeOsProbe`).
 */
export function detectRuntimeOs(probe: RuntimeOsProbe = {}): string {
  const platform = probe.platform;
  if (!platform) return "unknown";
  const fileExists = probe.fileExists ?? (() => false);
  const readTextFile = probe.readTextFile ?? (() => undefined);

  if (platform === "win32") return "windows";
  if (platform === "darwin") return "macos";

  if (platform === "linux") {
    const distro = readLinuxDistroId(readTextFile);
    const inDocker = isDockerRuntime(fileExists, readTextFile);
    if (inDocker) return distro ? `docker (${distro})` : "docker";
    return distro ? `linux (${distro})` : "linux";
  }

  return platform || "unknown";
}

function isDockerRuntime(
  fileExists: (path: string) => boolean,
  readTextFile: (path: string) => string | undefined,
): boolean {
  if (fileExists("/.dockerenv")) return true;
  const cgroup = readTextFile("/proc/1/cgroup");
  if (!cgroup) return false;
  return /docker|containerd|kubepods|podman/i.test(cgroup);
}

function readLinuxDistroId(readTextFile: (path: string) => string | undefined): string | undefined {
  const osRelease = readTextFile("/etc/os-release") ?? readTextFile("/usr/lib/os-release");
  if (!osRelease) return undefined;
  const idMatch = osRelease.match(/^ID=(.*)$/m);
  if (!idMatch?.[1]) return undefined;
  return normalizeDistroId(idMatch[1]);
}

function normalizeDistroId(raw: string): string {
  const unquoted = raw.trim().replace(/^["']|["']$/g, "").toLowerCase();
  const aliases: Record<string, string> = {
    ubuntu: "ubuntu",
    debian: "debian",
    centos: "centos",
    rhel: "rhel",
    fedora: "fedora",
    arch: "arch",
    manjaro: "manjaro",
    opensuse: "opensuse",
    sles: "sles",
    alpine: "alpine",
    amzn: "amazon-linux",
    ol: "oracle-linux",
    rocky: "rocky",
    alma: "almalinux",
    pop: "pop",
    linuxmint: "linuxmint",
  };
  return aliases[unquoted] ?? unquoted;
}

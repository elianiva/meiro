export const osNames = { mac: "macOS", win: "Windows", linux: "Linux" };
export type OS = keyof typeof osNames;

/** The visitor's desktop platform, so their download comes first. */
export function detectOS(): OS | null {
  const nav = navigator as Navigator & { userAgentData?: { platform: string } };
  const platform = nav.userAgentData?.platform || navigator.platform || "";
  const agent = navigator.userAgent || "";
  if (/mac/i.test(platform) || /Mac OS X/.test(agent)) return "mac";
  if (/win/i.test(platform) || /Windows/.test(agent)) return "win";
  if (/linux|x11/i.test(platform) || /Linux/.test(agent)) return "linux";
  return null;
}

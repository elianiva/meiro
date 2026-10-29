// The queue in the player preview. `h` and `c` are the hue and chroma the
// page takes from the cover; `yt` is the video the hidden embed plays.
export const songs = [
  { title: "イグニッション", artist: "*Luna feat. ゆある&ねんね", len: 214, cover: "/assets/screens/luna-cover.webp", yt: "6_Za11oioDY", h: 225, c: 0.1 },
  { title: "あの夏のいつかは", artist: "*Luna feat. ねんね", len: 340, cover: "/assets/screens/queue-ano.webp", yt: "igg0iHG8MR4", h: 75, c: 0.12 },
  { title: "アトラクトライト", artist: "*Luna feat. ゆある", len: 275, cover: "/assets/screens/queue-attract.webp", yt: "AVRF8B504GE", h: 265, c: 0.14 },
  { title: "冒険の書", artist: "*Luna feat. ゆある&ねんね", len: 227, cover: "/assets/screens/queue-adventure.webp", yt: "-XGTZVcQ3nM", h: 12, c: 0.1 },
];

export function clock(seconds: number) {
  const s = Math.floor(seconds);
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

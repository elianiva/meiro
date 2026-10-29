// The preview player's state, shared by every copy of the Now Playing screen.
// It follows the app: shuffle arranges the queue behind the current song,
// repeat goes from off to the queue to the track, and previous restarts a song
// that has played for more than 3 seconds. Audio comes from a hidden YouTube
// embed; when that cannot load, a timer stands in for it.
import { songs } from "../data/songs";

declare global {
  interface Window {
    YT?: any;
    onYouTubeIframeAPIReady?: () => void;
  }
}

export type Repeat = "off" | "queue" | "track";

export const state = {
  /** Songs in play order, as indexes into `songs`. */
  queue: songs.map((_, i) => i),
  /** The place in `queue` of the song playing. */
  index: 0,
  elapsed: 0,
  playing: false,
  shuffle: false,
  repeat: "off" as Repeat,
  autoPlay: true,
  liked: new Set<number>(),
  volume: 72,
  lens: songs.map((song) => song.len),
};

/** The order the queue held before shuffle arranged it. */
let kept: number[] = [];

export const current = () => state.queue[state.index];
export const remaining = () => state.queue.length - state.index - 1;
/** Whether Next has somewhere to go. */
export const hasNext = () => remaining() > 0 || state.repeat === "queue" || state.autoPlay;

const listeners: (() => void)[] = [];
export function subscribe(fn: () => void) {
  listeners.push(fn);
  fn();
}
function changed() {
  listeners.forEach((fn) => fn());
}

/* YouTube */
let yt: any = null;
let ytReady = false;
let ytFailed = false;

function loadYT() {
  if (yt || ytFailed) return;
  const build = () => {
    yt = new window.YT.Player("yt", {
      width: 1,
      height: 1,
      videoId: songs[current()].yt,
      playerVars: { playsinline: 1, controls: 0, disablekb: 1, rel: 0 },
      events: {
        onReady: (e: any) => {
          ytReady = true;
          e.target.setVolume(state.volume);
          // The song may have changed while the embed loaded.
          const id = songs[current()].yt;
          if (state.playing) e.target.loadVideoById(id, state.elapsed);
          else e.target.cueVideoById(id, state.elapsed);
        },
        onStateChange: (e: any) => {
          if (e.data === 0) ended();
        },
        onError: () => {
          ytFailed = true;
        },
      },
    });
  };
  if (window.YT?.Player) return build();
  window.onYouTubeIframeAPIReady = build;
  const tag = document.createElement("script");
  tag.src = "https://www.youtube.com/iframe_api";
  tag.onerror = () => {
    ytFailed = true;
  };
  document.head.appendChild(tag);
  setTimeout(() => {
    if (!yt) ytFailed = true;
  }, 8000);
}
const live = () => yt && ytReady && !ytFailed;

/* Transport */
function start(index: number, play = true) {
  state.index = index;
  state.elapsed = 0;
  const song = songs[current()];
  document.documentElement.style.setProperty("--h", String(song.h));
  document.documentElement.style.setProperty("--c", String(song.c));
  if (play) {
    state.playing = true;
    if (live()) yt.loadVideoById(song.yt);
    else if (!yt) loadYT();
  } else if (live()) {
    yt.cueVideoById(song.yt);
  }
  changed();
}

export function setPlaying(on: boolean) {
  state.playing = on;
  if (on && !yt) loadYT();
  if (live()) {
    if (on) yt.playVideo();
    else yt.pauseVideo();
  }
  changed();
}

/** The place of the song after the current one, with auto-play bringing back
 * songs that left the queue, as the app adds similar music. */
function following(): number | null {
  if (state.index + 1 < state.queue.length) return state.index + 1;
  if (state.repeat === "queue") return 0;
  if (!state.autoPlay) return null;
  const missing = songs.map((_, i) => i).filter((i) => !state.queue.includes(i));
  if (!missing.length) return 0;
  state.queue.push(...missing);
  return state.index + 1;
}

export function next() {
  const n = following();
  if (n !== null) start(n, true);
}

export function previous() {
  if (state.index === 0 || state.elapsed > 3) seek(0, true);
  else start(state.index - 1, true);
}

function ended() {
  if (state.repeat === "track") {
    seek(0, true);
    setPlaying(true);
    return;
  }
  const n = following();
  if (n !== null) start(n, true);
  else {
    state.elapsed = 0;
    setPlaying(false);
  }
}

export function seek(seconds: number, commit: boolean) {
  state.elapsed = Math.max(0, Math.min(state.lens[current()], seconds));
  if (commit && live()) yt.seekTo(state.elapsed, true);
  changed();
}

/** Plays a song from the queue. */
export function play(song: number) {
  start(state.queue.indexOf(song), true);
}

/* Queue */
export function toggleShuffle() {
  const now = current();
  if (!state.shuffle) {
    kept = [...state.queue];
    const rest = state.queue.filter((i) => i !== now);
    for (let i = rest.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [rest[i], rest[j]] = [rest[j], rest[i]];
    }
    state.queue = [now, ...rest];
  } else {
    // Back to the order before, keeping removals and placing new songs after.
    const before = kept.filter((i) => state.queue.includes(i));
    state.queue = [...before, ...state.queue.filter((i) => !before.includes(i))];
  }
  state.index = state.queue.indexOf(now);
  state.shuffle = !state.shuffle;
  changed();
}

export function cycleRepeat() {
  state.repeat = ({ off: "queue", queue: "track", track: "off" } as const)[state.repeat];
  changed();
}

export function toggleAutoPlay() {
  state.autoPlay = !state.autoPlay;
  changed();
}

export function remove(song: number) {
  const at = state.queue.indexOf(song);
  if (at < 0 || at === state.index) return;
  state.queue.splice(at, 1);
  if (at < state.index) state.index--;
  changed();
}

export function clearUpcoming() {
  state.queue = state.queue.slice(0, state.index + 1);
  changed();
}

export function toggleLike() {
  const song = current();
  if (!state.liked.delete(song)) state.liked.add(song);
  changed();
}

/* Volume */
let unmuted = state.volume;

export function setVolume(v: number) {
  state.volume = Math.round(Math.max(0, Math.min(100, v)));
  if (state.volume > 0) unmuted = state.volume;
  if (live()) yt.setVolume(state.volume);
  changed();
}

/** Silences the player, or brings back the last volume above 0. */
export function toggleMute() {
  setVolume(state.volume > 0 ? 0 : unmuted || 40);
}

/* The clock: follow the embed, or count on our own when it cannot play. */
let stall = 0;
export let dragging = false;
export function setDragging(on: boolean) {
  dragging = on;
}
setInterval(() => {
  if (!state.playing || dragging) {
    stall = 0;
    return;
  }
  if (live() && yt.getPlayerState?.() === 1) {
    stall = 0;
    state.elapsed = yt.getCurrentTime();
    const d = yt.getDuration();
    if (d > 30) state.lens[current()] = d;
  } else if (ytFailed) {
    state.elapsed += 0.25;
    if (state.elapsed >= state.lens[current()]) return ended();
  } else if (++stall > 40) ytFailed = true;
  else return;
  changed();
}, 250);

start(0, false);

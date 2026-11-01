# AI-generated song filter

A user can tell Meiro to skip songs YouTube marks as made with AI. The choice
survives a restart, applies to playback and auto-play, and does not stop the
track already playing.

## Sub-features

- `settings-ai-filter` searches for the setting, toggles
  `Exclude AI-generated songs`, and saves the choice.
- `play-ai-filter` checks a track before audio resolution, skips a labelled
  track, and plays the next unmarked track.
- `queue-ai-filter` removes known marked tracks that have not started when the
  setting is enabled or reloaded.
- `play-ai-filter-control` plays the same labelled track when the setting is
  off and makes no AI-label request.
- `pending-ai-filter` holds a label lookup across multiple playback polls,
  shows the chosen song as loading, and drops the lookup after another song
  is chosen or Next reaches the end of the queue.
- `preload-ai-filter` enables the filter with an excluded song already
  decoding or waiting to decode and an unchecked song following it.
- `run-ai-filter` checks every queued song, including more than four labelled
  songs in a row, and stops an entirely labelled repeat queue.

## How to get to it (user POV)

- Open `Settings`, click `Search settings`, and enter `artificial intelligence`.
- Turn on `Exclude AI-generated songs`.
- Return to an album, playlist, search result, or queue and choose a song
  YouTube labels as made with AI.

The setting is off by default. Its description says that YouTube does not label
every AI-generated track, so Meiro cannot guarantee that unlabelled AI music is
excluded.

## Driving it with the headless tester

Preconditions:

- `a, r := playbackApp(t)`.
- Mark the fake track with `markFakeAI("ai-track")`.
- Open `/album/MPREb_ai`, whose first row is `AI Track` and second row is
  `Human Track`.
- Set `a.settings.ExcludeAIGenerated = true` for the behaviour scenario, or
  leave it false for the control.

Steps:

1. Build `ui.NewTester(a.view, 1000, 700)` and click `AI Track`.
2. With the filter on, assert that `a.current.VideoID == "human-track"`,
   `r.asked("ai-track")` is false, and `r.asked("human-track")` is true.
3. Assert that `a.page.playable` no longer contains `ai-track`.
4. With the filter off, assert that `ai-track` plays, its audio resolution was
   asked for, and `aiChecks` stayed zero.
5. For persistence, set `a.saver.Path`, toggle the switch through Settings
   search, wait for the writer, and read the file with `settings.Load`.

Regression scenarios:

- Use `tonesApp(t, 1)`, open `/playlist/PL_songs`, enable the filter as a
  precondition, and click `Ameiro`. Hold subsequent work with `deferredRunner`,
  wait for the audio to end, and poll several times. The queue must stay on
  `song-two` and show loading until its label and audio arrive. Assert that
  history records `song-one` and `song-two`, with no resolve of `song-three`.
- Use `tonesApp`, remember an AI label for `song-two`, leave the filter off,
  and play `Ameiro`. Once `song-two` is preloaded (also test holding that work
  in flight), enable the filter through Settings search. Assert that the
  preload is cancelled and that the player never advances to `song-two`.
  Playback must continue with `song-three`.
- Mark `ai-one`, `ai-two`, `ai-three`, and `ai-four`, then open
  `/album/MPREb_ai_run` and click `ai-one`. The fifth song, `human`, must play.
  Mark it too to test entirely excluded queues with repeat off, queue repeat,
  and track repeat. Each of the five labels is requested once, with no audio
  resolved for an excluded song.
- `aiQueueMusic` supplies fresh AI-only recommendation batches and can put an
  allowed song at the end of the second batch. The allowed song must play;
  an endless supply of excluded recommendations must stop requesting batches.
- Set `switchable.mode = "server"` after the album loads, then choose a song
  with the filter on. Its audio must still resolve and its label stay unknown.

## Gotchas

- YouTube Music browse and search rows do not currently carry the AI label.
  Meiro learns it from the regular YouTube watch-page response immediately
  before playback. Do not assert that a never-checked row is absent from a
  list.
- `AIUnknown` means no determination was available. It must not be treated as
  human-made or excluded.
- The regular YouTube watch page carries an AI badge on the primary video, not
  necessarily in a related-video badge. The parser must only trust the primary
  `videoPrimaryInfoRenderer`.
- When the filter is on, Meiro does not preload an unlabelled next track. The
  label check must happen before audio starts.
- A failed label request fails open: the selected track plays rather than
  turning a network failure into a playback failure.
- A label check is part of starting the selected song. Polls must not process
  the previous stream's end or failure while that check is pending.
- AI exclusions do not consume the audio failure limit. A full queue pass
  bounds repeats; three new recommendation batches without any allowed song
  starting bound autoplay requests.
- Cancel obsolete preloads before returning for an unchecked next song. A
  removed song can still be held by the player or by a background task.

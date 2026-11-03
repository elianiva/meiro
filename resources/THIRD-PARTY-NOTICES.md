# Third-party software

The full release of Meiro ships three programs next to the app. The minimal
release does not bundle these programs; it uses the system's ffmpeg, yt-dlp,
and Node.js or Bun.

## ffmpeg

Decodes the audio. This is [FFmpeg](https://ffmpeg.org) built from source
with only the parts Meiro needs (`scripts/build-ffmpeg.sh` has the exact
configuration), licensed under the GNU Lesser General Public License version 3
(the text is in `licenses/ffmpeg-COPYING.LGPLv3`). It links OpenSSL on Linux,
under the Apache License 2.0, and uses the system's Secure Transport on macOS.
The source of the version shipped is at
<https://ffmpeg.org/releases/>. FFmpeg is a trademark of Fabrice Bellard.

## yt-dlp

Finds a stream to play and reads your browser's YouTube session. This is the
standalone build from <https://github.com/yt-dlp/yt-dlp>, released into the
public domain under the Unlicense. It bundles other free software; its
repository lists it.

## MeCab IPAdic

Supplies the dictionary reading of a Japanese lyric line. This is the IPA
dictionary packaged by [kagome-dict](https://github.com/ikawaha/kagome-dict),
used under its dictionary terms. A reading is a dictionary guess, so a word
with more than one reading may not match the one the song uses. Korean
readings are computed from the letters and do not use this dictionary.

## QuickJS

Runs the JavaScript that yt-dlp needs to solve YouTube's challenges. This is
the QuickJS engine from <https://github.com/quickjs-ng/quickjs>, licensed under
the MIT License (the text is in `licenses/quickjs-MIT.txt`).

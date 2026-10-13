# Third-party software

A release of Meiro ships three programs next to the app. Meiro runs them as
separate processes.

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

## QuickJS

Runs the JavaScript that yt-dlp needs to solve YouTube's challenges. This is
the QuickJS engine from <https://github.com/quickjs-ng/quickjs>, licensed under
the MIT License (the text is in `licenses/quickjs-MIT.txt`).

# Third-party software

The full release of Meiro ships three programs next to the app. The minimal
release does not bundle these programs; it uses the system's ffmpeg, yt-dlp,
and Node.js or Bun. The app downloads a Japanese dictionary on first use, for the Latin
spelling of Japanese lyrics; it is described below.

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

## Japanese dictionary

Reads the kanji in Japanese lyrics for their Latin spelling. Meiro downloads
this the first time romanization is used, from
[Kagome's IPADIC dictionary](https://github.com/ikawaha/kagome-dict) at the
pinned `ipa/v1.2.6` commit, and checks it against a known SHA-256. This is
[Kagome](https://github.com/ikawaha/kagome) and its IPADIC dictionary, both
under the MIT License, embedding data from mecab-ipadic-2.7.0-20070801.

Kagome is a Go port of [Kuromoji](https://github.com/atilika/kuromoji), and
the romanization follows
[Kuroshiro](https://github.com/hexenq/kuroshiro).

### mecab-ipadic-2.7.0-20070801 notice

Nara Institute of Science and Technology (NAIST),
the copyright holders, disclaims all warranties with regard to this
software, including all implied warranties of merchantability and
fitness, in no event shall NAIST be liable for
any special, indirect or consequential damages or any damages
whatsoever resulting from loss of use, data or profits, whether in an
action of contract, negligence or other tortuous action, arising out
of or in connection with the use or performance of this software.

A large portion of the dictionary entries
originate from ICOT Free Software. The following conditions for ICOT
Free Software applies to the current dictionary as well.

Each User may also freely distribute the Program, whether in its
original form or modified, to any third party or parties, PROVIDED
that the provisions of Section 3 ("NO WARRANTY") will ALWAYS appear
on, or be attached to, the Program, which is distributed substantially
in the same form as set out herein and that such intended
distribution, if actually made, will neither violate or otherwise
contravene any of the laws and regulations of the countries having
jurisdiction over the User or the intended distribution itself.

NO WARRANTY

The program was produced on an experimental basis in the course of the
research and development conducted during the project and is provided
to users as so produced on an experimental basis. Accordingly, the
program is provided without any warranty whatsoever, whether express,
implied, statutory or otherwise. The term "warranty" used herein
includes, but is not limited to, any warranty of the quality,
performance, merchantability and fitness for a particular purpose of
the program and the nonexistence of any infringement or violation of
any right of any third party.

Each user of the program will agree and understand, and be deemed to
have agreed and understood, that there is no warranty whatsoever for
the program and, accordingly, the entire risk arising from or
otherwise connected with the program is assumed by the user.

Therefore, neither ICOT, the copyright holder, or any other
organization that participated in or was otherwise related to the
development of the program and their respective officials, directors,
officers and other employees shall be held liable for any and all
damages, including, without limitation, general, special, incidental
and consequential damages, arising out of or otherwise in connection
with the use or inability to use the program or any product, material
or result produced or otherwise obtained by using the program,
regardless of whether they have been advised of, or otherwise had
knowledge of, the possibility of such damages at any time during the
project or thereafter. Each user will be deemed to have agreed to the
foregoing by his or her commencement of use of the program. The term
"use" as used herein includes, but is not limited to, the use,
modification, copying and distribution of the program and the
production of secondary products from the program.

In the case where the program, whether in its original form or
modified, was distributed or delivered to or received by a user from
any person, organization or entity other than ICOT, unless it makes or
grants independently of ICOT any specific warranty to the user in
writing, such person, organization or entity, will also be exempted
from and not be held liable to the user for any such damages as noted
above as far as the program is concerned.

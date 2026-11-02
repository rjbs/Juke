# Album art

Art is an image per album, and a track's own image where its file's
picture differs from its album's.  Nothing in Apple's now-playing API or
in JMAP asks for the album to be the unit; it is the unit because that
is what a library of albums has, and because a picture per track would
be the same few thousand pictures over and over.  (#3)

## Where images come from

- **Embedded pictures.**  The import stores a stub for each picture a
  file carries: frame, type, MIME type, size and SHA-256 (see
  [import](import.md)).  So which files carry which pictures is known
  from the database, and only a picture not yet stored is read out of a
  file.
- **Folder images.**  An image file beside an album's tracks named
  `cover`, `folder`, `front`, `artwork` or `album` is the album's cover.
  Other image files in the directory are not assumed to be art; the
  recovery left web-page scraps in a few.
- **Only front covers count.**  A picture typed as a back cover, a
  booklet page or the like is not a candidate.  A picture with no type,
  which is what iTunes writes and what MP4 files have, is a front cover.
  A file with several front covers contributes its first.

## Whose an image is

For each album, in order:

1. A folder image is the album's.
2. Otherwise the front cover on the most tracks is.  A track with no
   picture abstains, and inherits the result.
3. On a tie, the first track's, by disc and position.  A singles
   collection with a different picture on every track gets its first
   track's as a cover rather than none, and a re-run lands on the same
   answer.

Then any track whose own front cover differs from its album's image
keeps its own, as `artworkBlobId` on the Track.  A track on no album
keeps its own picture the same way.  A track whose picture matches its
album's, or that has none, has a null `artworkBlobId`, and a client
shows its album's.

Of the library's 3,985 albums as imported, 1,722 have no embedded art,
1,661 agree on one image, 229 agree where tracks have one at all, and
373 disagree.  Among the 373, 115 carry two images, 159 a different one
on every track, and the rest something between.  A tool to look at
those and set an album's image by hand is a thread for later.

## Storage and serving

Each distinct image is stored once, as a file under `artwork/` in the
library directory, named by its id, which is derived from the image's
SHA-256 so the same picture from any file is one image.  The `artwork`
table records each image's media type, size and pixel size.

An image is a blob: its id is the `artworkBlobId` on Album and Track,
and the JMAP download URL serves it with its media type.  Clients fetch
and cache it as they do audio, and since the id names the bytes, it is
cacheable forever.  A track's `artworkBlobId` names its own image only
when it has one, so a client resolves `track.artworkBlobId ??
album.artworkBlobId`.

The pictures stay in the audio files too; the served audio is the
original, untouched (see [storage](storage.md)).  Making every file
carry its album's art is a separate job for the ID3 fix-up (#4).

## Running it

    var/juke artwork --db var/juke.sqlite --library /srv/juke

It is safe to run again, and to run while the server is up: albums and
tracks whose art changes get one new modseq per type, so clients
refetch them once, and the server notices on its next poll.  Reading
pictures out of the files happens outside the transaction that assigns
them, so writers aren't held off for the walk.

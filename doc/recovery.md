# Recovery and reorganization

Before Juke exists, the library has to be put back together.  This is the
plan for that, with the decisions made so far.  The outcome is one
human-readable tree of music, archived immutably, which Juke's initial import
will read.

Background: a 2014 NAS migration lost roughly a tenth of the library, and
scattered individual tracks besides.  The write-up is at
<https://rjbs.cloud/blog/2014/06/just-how-much-data-did-i-lose/>.  The rules
below are mostly that post's lessons made explicit.

## Inputs

- **The original library**, copied from the NAS to `/Volumes/Video/music/music-volume/music`.
  Roughly 160 GB.  This is the library the 2014 loss happened to.  It
  contains an `index.txt` from January 2019, a `find` listing of the tree at
  that time.
- **The 2014 iTunes XML**, a backup of the iTunes library file from before
  the data loss.  It lists every file that existed then, with play counts
  and ratings, and possibly playlists.
- **The current Apple Music library** in `~/Music/Music`.  Some lost music
  was re-imported after 2014, but Apple Music was mostly abandoned at that
  point, so it holds little new rating or play-count data.
- **A second library**, at `/Volumes/Video/music/jcap-music`, roughly 140 GB.
  It belongs to a partner and is safe to merge.  Some of it may stand in for
  files missing from the original.

Both libraries have a nested `Music/` directory inside them, the iTunes
"new-style" location that caused trouble in 2014.  Expect it to hold tracks
that belong beside, not under, the rest of the tree.

## Rules

- **Never destroy a source until a second, independent comparison confirms
  the copy.**  Every reorganizing move is a copy, then a hash comparison of
  source and destination, then a delete, as three separate steps.
- **Catalog by content, not by path.**  Files are identified by a hash of
  their audio frames, with ID3 tags excluded, which is the same track-id rule
  Juke will use.  See [storage](storage.md).
- **Filenames are bytes until proven otherwise.**  The catalog records names
  as stored and flags any that aren't valid UTF-8 or look double-encoded.
  Names stay Unicode in the target tree; the work happens on APFS, and
  "Dos Anos" is not an acceptable artist name.
- **Nested trees and partial albums are findings, not errors.**  Tools report
  them; nothing is fixed by hand mid-run.

## Steps

1. **Catalog what existed.**  Parse the 2014 XML into a catalog: tracks with
   artist, album, title, track number, path, play count and rating, plus
   playlists.  Done.
2. **Inventory what exists now.**  Walk both library trees and record every
   file with its path, size, audio hash, and tag summary.  Done.
3. **Match and report.**  Link each catalog track to a file where one
   survives, and report per album what is present and what is missing.
   Done; the report is `/Volumes/Video/music/missing.tsv`.
4. **Split into two libraries.**  Every album goes to one of two trees:
   - **Undamaged:** every track the 2014 catalog lists for the album has a
     file, or the album was never in the catalog at all.  This is the basis
     for the initial Juke library.
   - **Damaged:** at least one catalog track has no file.  The surviving
     tracks go here too.  This tree is a backup and a queue of albums to
     replace over time, and it does not go into Juke.

   Both trees are iTunes-shaped: `Artist/Album/NN Title.ext`, with
   `Compilations/Album/` for various-artists albums.  The 2014 problems
   came from iTunes moving files one at a time as they played, not from the
   layout itself.
5. **Merge the second library.**  When both libraries have an album, keep
   the original.  When the second library has a whole album and the original
   has only part of it, take the second library's copy, which makes that
   album undamaged.  Albums only the second library has go straight to
   undamaged.  When two different rips of one track survive, from any
   source, keep the higher bitrate.  The CDs are gone since 2018, so no rip
   can be redone.
6. **Fix tags.**  Deferred until the files are organized.
7. **Archive.**  Write a manifest of audio hashes and tag summaries into
   each tree, then make them immutable.  That manifest is the second
   comparison, made permanent, and the input to Juke's import.

## Where things live

- Scripts live in this repository.
- Catalog data lives on the SSD under `/Volumes/Video/music`.
- The library contains M4A and probably M4P alongside MP3.  What else is in
  there is a question for the inventory.

## Running it

The catalog is `/Volumes/Video/music/catalog.sqlite`.  Every tool takes
`--db` pointing at it.  In order:

    bin/import-itunes-xml --xml Pre-Oops-Library.xml --db $DB
    bin/inventory-files --db $DB --root /Volumes/Video/music/music-volume/music --library mine -v
    bin/inventory-files --db $DB --root /Volumes/Video/music/jcap-music --library jcap -v
    bin/match-tracks --db $DB --library mine
    bin/match-tracks --db $DB --library jcap
    bin/group-albums --db $DB
    bin/report-missing --db $DB --library mine --fallback jcap > /Volumes/Video/music/missing.tsv
    bin/report-incomplete --db $DB --library mine > /Volumes/Video/music/incomplete-2014.tsv

The import and the matcher rebuild their tables from scratch.  The
inventory is incremental and skips files whose size and mtime haven't
changed, so rerunning it after a reorganization only reads what moved.  The
full first run of both trees took about two hours, limited by the drive.

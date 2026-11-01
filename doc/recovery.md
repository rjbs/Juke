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
    bin/report-incomplete --db $DB --library mine --dispositions /Volumes/Video/music/album-dispositions.tsv > /Volumes/Video/music/incomplete-2014.tsv
    bin/plan-library --db $DB --dispositions /Volumes/Video/music/album-dispositions.tsv --out /Volumes/Video/music/plan.tsv
    bin/apply-plan --db $DB --dest /Volumes/Video/library \
      --library mine=/Volumes/Video/music/music-volume/music \
      --library jcap=/Volumes/Video/music/jcap-music --dry-run

The import, the matcher, the grouping and the planner rebuild their
tables from scratch.  The mover records every rename in a move_log table
and skips anything already there, so it can be rerun after an interruption.
After the move, inventory each new root (`--library Undamaged` and so on)
to re-hash everything as verification; that inventory is the archive
manifest.  The
inventory is incremental and skips files whose size and mtime haven't
changed, so rerunning it after a reorganization only reads what moved.  The
full first run of both trees took about two hours, limited by the drive.

Decisions about individual albums live in
`/Volumes/Video/music/album-dispositions.tsv`: artist, album, disposition,
note.  Dispositions so far are `keep` (whole, whatever the tags claim),
`damaged`, `misc` (not music), `audiobook`, and `jcap` (take the partner's
copy).  The incomplete report leaves out any album listed there, and the
planner honors the same file.

## Status

The move ran on 2026-10-14, and was undone and redone twice the same day
as review of the leave-behinds found planner and matcher bugs (byte-size
and generic-name coincidences, same-named albums merged into a
compilation, box sets without disc numbers losing tracks, a stray match
renaming an album).  The third run moved 54,405 files into the four
roots under `/Volumes/Video/library`: Undamaged 44,949, Damaged 5,422,
Audiobooks 3,110, Miscellaneous 924; 352 albums are damaged.  The old
trees under `/Volumes/Video/music` are hollow: 1,444 files of mine and
3,995 of the partner's remain, which are byte-identical duplicates, the
same recording at a lower bitrate, other editions of albums mine has
whole, artwork whose audio did not move, and files the inventory never
looked at (.DS_Store, iTunes LP bundles, booklets, app downloads).  The
`move_log` table in the catalog maps every file's old path to its new
one, and `apply-plan --undo` reverses it; `move_plan.displaced_by` says
which file won each slot a left-behind file lost, which the ratings
transfer will need.

The four new roots were then inventoried as the archive manifest, with
the `file` table rows for libraries `Undamaged`, `Damaged`, `Audiobooks`
and `Miscellaneous`.  Every logged move was checked against that fresh
inventory: all 54,405 files are present, each with the size and audio
hash its old row recorded (artwork, which has no audio hash, matched on
size).  One wrinkle: 475 files in 56 planned directories sit at a path
that differs from `move_log.dest` only in case, because APFS is
case-insensitive and two units spelled the artist differently (Beck and
beck, Dntel and DNTEL, Nas and NaS); the later unit landed in the earlier
one's directory.  Nothing was lost.  Those directories were then renamed
to the catalog's spelling, or left as found where the catalog had none,
and `move_log`, `move_plan` and the inventory rows rewritten to match, so
every logged move again names the exact path on disk.  The planner should
still fold case when naming directories (loose thread 2026-10-14-k4zwgt).  Next is reviewing Damaged.

On 2026-11-01 the superseded part of Damaged was set aside: the 186
files planned as `partial copy; whole album from jcap` and the 3
`replaced by jcap's copy`, all of them partial copies of albums the
library has whole.  `bin/set-aside-redundant` renamed them to the same
paths under a fifth root, `Redundant`, hashing each before and after;
`move_log.dest` was updated to say where each now is, and the new
`relocation_log` table records the step, so `--undo` reverses it.  The
rest of Damaged is imported into Juke beside Undamaged, its lost tracks
as gaps (#41).

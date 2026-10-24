# Sample library for prototyping

The full library is 258 GB and 45,000 files, too big to re-import every
time the importer changes.  The prototype works from a sample of about
1 GB, plucked from the real library so it has the real library's
problems.  It has 189 files and comes to 958 MB.  It is copied, never
moved, from `/Volumes/Video/library`; the archive stays untouched.

Each album below was picked for a case the data model or the importer has
to handle.  Many cover more than one.  Sizes come from the inventory and
are approximate.

## Cases

| Case | Pick | Files | MB |
|------|------|------:|---:|
| Plain album, ratings including half stars, play counts | Weird Al Yankovic, *Off the Deep End* | 11 | 62 |
| AAC in M4A, every track rated | Johnny Cash, *American II: Unchained* | 14 | 42 |
| Two discs tagged as one album | Queen, *Live Killers* | 22 | 84 |
| Two discs filed as two albums, each disc named | Smashing Pumpkins, *Mellon Collie and the Infinite Sadness*, "Dawn to Dusk" and "Twilight to Starlight" | 28 | 149 |
| Two discs filed as two albums, the second marked "(Disc 2)"; non-ASCII artist | µ-Ziq, *Tango N' Vectif* | 26 | 133 |
| Various-artists compilation | *We Are Not Devo* | 13 | 53 |
| Album artist naming two artists, differing from track artists; non-ASCII | Stan Getz and João Gilberto, *Getz/Gilberto* | 10 | 90 |
| Same album title, different artists | 2Pac and Ice Cube, each *Greatest Hits* | 4 | 15 |
| No artist, no track numbers | *Invader Zim*, filed under `_` | 4 | 8 |
| Album tags missing from the files, known to the 2014 catalog | De La Soul, *3 Feet High and Rising* | 23 | 115 |
| Damaged: one gap mid-album (track 4 of 10) | Save Ferris, *It Means Everything* | 7 | 22 |
| Damaged: two gaps (tracks 4 and 6 of 10) | Neil Young, *Comes a Time* | 8 | 29 |
| Non-ASCII: diaeresis | Queensrÿche | 5 | 20 |
| Non-ASCII: Japanese; one disc of a box set | 山根麻衣, *Cowboy Bebop CD-Box* [Disc 3] | 1 | 6 |
| Mis-decoded tags (Musiał read as Musia³) | Piotr Musiał, *Anomaly Korea* | 4 | 12 |
| Large single file, for range requests and streaming | John Digweed mix, no album | 1 | 80 |
| One-track "album" with no album tag | Monty Python, "Brave Sir Robin" | 1 | 1 |
| Cover art as a loose JPEG; artist and album share a name; M4A | Black Sabbath, *Black Sabbath* | 6 | 36 |
| Untagged file, which the importer must refuse or quarantine | "Danger Mouse.mp3", from `Miscellaneous/Untagged` | 1 | 1 |
| Total | | 189 | 958 |

## Building it

The sample lives in `sample/` at the top of this repository, which git
ignores.  `doc/sample-library.paths` lists each directory or file relative
to the library root, and the copy keeps those paths, so the sample has the
archive's layout:

    rsync -a -r --files-from=doc/sample-library.paths \
      /Volumes/Video/library/ sample/

The copy made on 2026-10-24 was checked byte for byte against the
originals.

## Playlists

The 2014 playlists are mostly short mixes drawn from dozens of albums, so
importing one faithfully would drag most of those albums in.  The sample
gets made-up playlists over its own tracks instead:

- one that spans several albums, including an M4A and a non-ASCII track,
- one that lists a track twice,
- one that includes a gap track, which the player must skip.

## Metadata

Ratings, play counts and last-played dates come from the 2014 catalog for
the tracks it matched, as the full import will.  The sample's slice of the
catalog goes with it, so the importer is exercised end to end.

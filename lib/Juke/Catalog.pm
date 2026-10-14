package Juke::Catalog;
use v5.36;

use Exporter 'import';
our @EXPORT_OK = qw(ensure_views);

=head1 NAME

Juke::Catalog - views over the imported iTunes catalog

=head1 FUNCTIONS

=head2 ensure_views

  ensure_views($dbh);

Creates, or recreates, the views the recovery tools share:

=over

=item itunes_audio_track

The rows of C<itunes_track> that are audio files on disk, minus duplicate
listings.  iTunes sometimes listed one file twice, under one path or as a
second copy with a C< 1> suffix.  A track is a duplicate listing if an
earlier track has the same path, or the same size, duration, album and
title.  One surviving file satisfies both listings, so the second is
nothing to look for and nothing to count as missing.

=back

=cut

sub ensure_views ($dbh) {
  $dbh->do('DROP VIEW IF EXISTS itunes_audio_track');
  $dbh->do(<<~'SQL');
    CREATE VIEW itunes_audio_track AS
    SELECT t.*
    FROM itunes_track t
    WHERE t.track_type = 'File'
      AND t.kind LIKE '%audio file'
      AND t.music_path IS NOT NULL
      AND NOT EXISTS (
        SELECT 1 FROM itunes_track o
        WHERE o.track_id < t.track_id
          AND o.track_type = 'File'
          AND o.kind LIKE '%audio file'
          AND o.music_path IS NOT NULL
          AND (   o.music_path = t.music_path
               OR (    o.size = t.size
                   AND o.total_time = t.total_time
                   AND o.album IS t.album
                   AND o.name IS t.name)))
    SQL
  return;
}

1;

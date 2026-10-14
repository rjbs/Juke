package Juke::Albums;
use v5.36;

use Juke::Names qw(norm_text);

use Exporter 'import';
our @EXPORT_OK = qw(albums_from_rows);

=head1 NAME

Juke::Albums - decide which tracks make up which albums

=head1 SYNOPSIS

  my @albums = albums_from_rows(\@rows);

  for my $album (@albums) {
    # $album->{artist}, $album->{album}, $album->{compilation},
    # $album->{singleton}, $album->{why}, $album->{members}
  }

=head1 DESCRIPTION

Both the iTunes catalog and a directory of files describe albums only by
the tags on individual tracks: the album name, the artist, the album
artist, the compilation flag, and the disc and track numbers.  An album
with sixteen artists and no flag looks like sixteen one-track albums, and
an album with the flag on half its tracks straddles two directories.
This module groups rows into albums the way a person would, and is used
on catalog tracks by C<group-albums> and on files by C<plan-library>, so
the two agree.

The rules, applied to all rows sharing an album name:

=over

=item *

A row with no album name, or "Unknown Album", is an album by itself, a
singleton.

=item *

If more than half the rows are flagged as a compilation, the flagged rows
are one, filed under "Compilations".  The unflagged rows are split by
artist; a part whose disc and track numbers are all free in the
compilation joins it, since it is a track that never got the flag, and a
part that collides is an album of its own.  That keeps Peter Gabriel's
I<Passion> out of Sondheim's.

=item *

Otherwise, if some rows are flagged and the track numbers are all
distinct, or if the group spans three or more artists with no album
artist and the track numbers are all distinct, the whole group is a
compilation.

=item *

Otherwise the group is split by album artist, or by artist where there is
none, and each part is an album.

=back

The "track numbers are all distinct" test is what keeps "Greatest Hits"
by forty artists from becoming one album.

=head1 FUNCTIONS

=head2 albums_from_rows

  my @albums = albums_from_rows(\@rows);

Each row is a hashref with C<album>, C<artist>, C<album_artist>,
C<compilation>, C<disc_number> and C<track_number>; other keys are
ignored and the rows are returned untouched as C<members>.  Each album
returned has C<artist> ("Compilations" for a compilation), C<album>,
C<compilation>, C<singleton>, C<why> (C<flagged> or C<inferred> for a
compilation, else undef) and C<members>.  Albums come out in a stable
order: singletons first in input order, then by album name.

=cut

sub albums_from_rows ($rows) {
  my (%by_name, @albums);

  for my $row (@$rows) {
    my $album = $row->{album} // '';

    if ($album eq '' || fc $album eq fc 'unknown album') {
      push @albums, {
        artist => $row->{artist} // '', album => '',
        compilation => 0, singleton => 1, members => [ $row ],
      };
    } else {
      push @{ $by_name{ norm_text($album) } }, $row;
    }
  }

  for my $name (sort keys %by_name) {
    push @albums, split_name_group($by_name{$name});
  }

  return @albums;
}

sub split_name_group ($group) {
  my $album     = $group->[0]{album};
  my @flagged   = grep {;   $_->{compilation} } @$group;
  my @unflagged = grep {; ! $_->{compilation} } @$group;

  if (@flagged > @$group / 2) {
    my %taken = map {; (position($_) // '') => 1 } @flagged;
    my @members = @flagged;
    my @separate;

    for my $part (by_artist(\@unflagged)) {
      my $collides = grep {; $taken{ position($_) // '' } } @{ $part->{members} };

      if ($collides) {
        push @separate, $part;
      } else {
        push @members, @{ $part->{members} };
        $taken{ position($_) // '' } = 1 for @{ $part->{members} };
      }
    }

    return (compilation($album, 'flagged', \@members), @separate);
  }

  my $distinct = numbers_distinct($group);

  return compilation($album, 'flagged', $group) if @flagged && $distinct;

  my %artists = map {; norm_text($_->{artist}) => 1 } @$group;
  my $album_artists = grep {; length($_->{album_artist} // '') } @$group;

  return compilation($album, 'inferred', $group)
    if keys %artists >= 3 && ! $album_artists && $distinct;

  return by_artist($group);
}

sub compilation ($album, $why, $members) {
  return {
    artist => 'Compilations', album => $album,
    compilation => 1, singleton => 0, why => $why, members => $members,
  };
}

sub by_artist ($rows) {
  my %part;
  for my $row (@$rows) {
    push @{ $part{ norm_text(artist_of($row)) } }, $row;
  }

  return map {;
    {
      artist => artist_of($part{$_}[0]), album => $part{$_}[0]{album},
      compilation => 0, singleton => 0, members => $part{$_},
    }
  } sort keys %part;
}

sub artist_of ($row) {
  return length($row->{album_artist} // '') ? $row->{album_artist} : ($row->{artist} // '');
}

sub position ($row) {
  return undef unless defined $row->{track_number};
  return join '-', ($row->{disc_number} // 1), $row->{track_number};
}

sub numbers_distinct ($group) {
  my %seen;
  for my $row (@$group) {
    my $position = position($row) // return 0;
    return 0 if $seen{$position}++;
  }
  return 1;
}

1;

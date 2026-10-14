package Juke::Names;
use v5.36;

use Unicode::Normalize qw(NFC);

use Exporter 'import';
our @EXPORT_OK = qw(norm_text album_key albums_alike safe_filename disc_and_track_from_name);

=head1 NAME

Juke::Names - comparing and sanitizing the names of things

=head1 FUNCTIONS

=head2 norm_text

  my $key = norm_text($string);

NFC, case-folded, whitespace collapsed and trimmed.  Undefined becomes the
empty string.  This is the comparison form for artists, albums and titles.

=cut

sub norm_text ($text) {
  return '' unless defined $text;
  $text = NFC($text);
  $text =~ s/\s+/ /g;
  $text =~ s/\A | \z//g;
  return fc $text;
}

=head2 album_key

  my $key = album_key($album);

A looser form of an album name for deciding whether two names mean the
same album: punctuation gone, disc and volume markers gone, so that
"Drukqs [Disc 1]" and "Drukqs" agree, as do "The Fragile - Right" and "The
Fragile (Right)".

=cut

sub album_key ($album) {
  my $key = norm_text($album);
  $key =~ s/\[[^\]]*\]|\([^)]*\)//g;
  $key =~ s/\b(?:disc|disk|cd|vol(?:ume)?\.?)\s*\d+\b//g;
  $key =~ s/\bdisc\s+\w+\b//g;
  $key =~ s/[^\p{Alnum}]+//g;
  return $key;
}

=head2 albums_alike

  if (albums_alike($name_a, $name_b)) { ... }

True if the two album names probably name the same album: their keys are
equal, or one key is a prefix of the other and the shorter is at least six
characters, so that "Led Zeppelin II" and "II" don't match but "Who Cares A
Lot" and "Who Cares A Lot? The Greatest Hits" do.  An empty name matches
nothing.

=cut

sub albums_alike ($a, $b) {
  my ($ka, $kb) = (album_key($a), album_key($b));
  return 0 unless length $ka && length $kb;
  return 1 if $ka eq $kb;

  my ($short, $long) = length $ka <= length $kb ? ($ka, $kb) : ($kb, $ka);
  return 0 if length $short < 6;
  return index($long, $short) == 0;
}

=head2 safe_filename

  my $component = safe_filename($name);

One path component: slashes and colons become underscores, as iTunes does,
control characters are dropped, whitespace is collapsed, and the result is
capped at 200 characters.  Never empty.

=cut

sub safe_filename ($name) {
  $name = NFC($name // '');
  $name =~ s{[/:]}{_}g;
  $name =~ s/[\x00-\x1f]//g;
  $name =~ s/\s+/ /g;
  $name =~ s/\A[\s.]+|\s+\z//g;
  $name = substr($name, 0, 200) if length $name > 200;
  return length $name ? $name : '_';
}

=head2 disc_and_track_from_name

  my ($disc, $track) = disc_and_track_from_name($basename);

iTunes names files "NN Title" or "D-NN Title".  This reads those numbers
back out of a filename, returning undef for whichever is absent.

=cut

sub disc_and_track_from_name ($basename) {
  return ($1, $2) if $basename =~ /\A(\d{1,2})-(\d{2,3}) /;
  return (undef, $1) if $basename =~ /\A(\d{2,3}) /;
  return (undef, undef);
}

1;

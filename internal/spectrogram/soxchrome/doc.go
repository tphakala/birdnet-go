// Package soxchrome holds the presentation parts of the SoX spectrogram image:
// the colour palette, the dB to palette index mapping, the image geometry, and
// the axes, ticks, tick labels and dBFS legend drawn around the raster.
//
// The code is derived from SoX src/spectrogram.c, (c) 2008-9
// robs@users.sourceforge.net, through github.com/tphakala/go-spectrogram (sox
// package, commit ebf0c64). This package alone is licensed under the GNU Lesser
// General Public License version 2.1 or later (see LICENSE in this directory),
// regardless of the license of the rest of BirdNET-Go.
//
// The package contains no SoX font data or glyph bitmaps. Text is drawn through
// the Surface interface, so the caller supplies the glyph shapes; only the
// 6 by 12 pixel text cell and the offsets measured in it come from SoX.
//
// All drawing coordinates are bottom-up: y = 0 is the bottom row of the image,
// as in SoX.
package soxchrome

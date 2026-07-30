package zmachine

import "testing"

func TestSignedAndUnsignedAreInverses(t *testing.T) {
	// S 2.2: numbers are held in signed 2's complement 16-bit form, so $ffff
	// and -1 are the same word read two ways.
	tests := []struct {
		word uint16
		want int16
	}{
		{word: 0x0000, want: 0},
		{word: 0x0001, want: 1},
		{word: 0x7fff, want: 32767},
		{word: 0x8000, want: -32768},
		{word: 0xffff, want: -1},
		{word: 0xff66, want: -154},
	}
	for _, tt := range tests {
		if got := signed(tt.word); got != tt.want {
			t.Errorf("signed(0x%04x) = %d, want %d", tt.word, got, tt.want)
		}
		if got := unsigned(tt.want); got != tt.word {
			t.Errorf("unsigned(%d) = 0x%04x, want 0x%04x", tt.want, got, tt.word)
		}
	}
}

// TestSignExtend covers the widths the engine reads signed values at: 14 bits
// for a two-byte branch offset (S 4.7) and 16 for an ordinary word (S 2.2).
func TestSignExtend(t *testing.T) {
	tests := []struct {
		name  string
		value uint16
		bits  uint
		want  int16
	}{
		{name: "14-bit zero", value: 0x0000, bits: 14, want: 0},
		{name: "14-bit largest positive", value: 0x1fff, bits: 14, want: 8191},
		{name: "14-bit most negative", value: 0x2000, bits: 14, want: -8192},
		{name: "14-bit minus one", value: 0x3fff, bits: 14, want: -1},
		{name: "14-bit minus two", value: 0x3ffe, bits: 14, want: -2},
		// Bits above the field are discarded rather than treated as data.
		{name: "14-bit ignores higher bits", value: 0xc001, bits: 14, want: 1},
		{name: "6-bit stays positive", value: 0x3f, bits: 6, want: -1},
		{name: "16-bit is unchanged", value: 0x8000, bits: 16, want: -32768},
		{name: "one bit", value: 0x01, bits: 1, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signExtend(tt.value, tt.bits); got != tt.want {
				t.Errorf("signExtend(0x%04x, %d) = %d, want %d", tt.value, tt.bits, got, tt.want)
			}
		})
	}
}
